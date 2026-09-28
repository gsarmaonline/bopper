package environment

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Docker reclaims nothing on its own that is useful here. It records no
// last-access time for a volume, it does not know which resources belong
// together, and its only automatic cleanup is BuildKit's build-cache collection.
// `docker system prune --filter until=` filters on CREATION time, not use, and
// does not apply to volumes at all.
//
// Bopper knows each workspace's lifecycle, so it can be precise instead: every
// resource it creates carries bopper.workspace=<id>, and cleanup touches only
// those. The baseline and other projects are never at risk.

// Resource is one thing Bopper created for a workspace.
type Resource struct {
	Kind      string // "container" or "network"
	Name      string
	Workspace string
	Started   time.Time
	Running   bool
}

// Policy decides what cleanup removes.
type Policy struct {
	// StopIdle stops an overlay whose workspace has not been reached in this
	// long. Memory is freed; data and the worktree are untouched.
	StopIdle time.Duration
	// RemoveIdle removes an overlay's containers and network after this long.
	RemoveIdle time.Duration
	// Known is the set of workspace IDs that still exist. Anything labelled for
	// a workspace outside this set is an orphan and is removed regardless of age.
	Known map[string]bool
	// DryRun reports without changing anything.
	DryRun bool
}

// Action is one decision cleanup made.
type Action struct {
	Resource Resource
	Verb     string // "stop", "remove", "keep"
	Reason   string
}

// LastSeen reads the proxy's access log and returns the most recent request
// time per host.
//
// A missing or unreadable log is not an error: a workspace with no routed
// service never appears in it, and a fresh install has no log at all. Callers
// fall back to container start time, which is a weaker but safe signal.
func (r *Runner) LastSeen() map[string]time.Time {
	out := map[string]time.Time{}
	f, err := os.Open(r.AccessLogPath())
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var rec struct {
			RequestHost string `json:"RequestHost"`
			StartUTC    string `json:"StartUTC"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil || rec.RequestHost == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, rec.StartUTC)
		if err != nil {
			continue
		}
		host := rec.RequestHost
		if i := strings.IndexByte(host, ':'); i > 0 {
			host = host[:i]
		}
		if t.After(out[host]) {
			out[host] = t
		}
	}
	return out
}

// Inventory lists every container and network Bopper created for a workspace.
func (r *Runner) Inventory(ctx context.Context) ([]Resource, error) {
	var res []Resource

	out, err := r.run(ctx, "docker", "ps", "-a", "--format",
		"{{.Names}}\t{{.Label \"bopper.workspace\"}}\t{{.State}}",
		"--filter", "label=bopper.workspace")
	if err != nil {
		return nil, err
	}
	for _, line := range splitLines(out) {
		f := strings.Split(line, "\t")
		if len(f) < 3 || f[1] == "" {
			continue
		}
		started := r.startedAt(ctx, f[0])
		res = append(res, Resource{
			Kind: "container", Name: f[0], Workspace: f[1],
			Started: started, Running: f[2] == "running",
		})
	}

	nets, err := r.run(ctx, "docker", "network", "ls", "--format", "{{.Name}}",
		"--filter", "name="+workspaceNetwork)
	if err != nil {
		return nil, err
	}
	for _, n := range splitLines(nets) {
		if id := strings.TrimPrefix(n, workspaceNetwork); id != n {
			res = append(res, Resource{Kind: "network", Name: n, Workspace: id})
		}
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Workspace != res[j].Workspace {
			return res[i].Workspace < res[j].Workspace
		}
		return res[i].Name < res[j].Name
	})
	return res, nil
}

func (r *Runner) startedAt(ctx context.Context, name string) time.Time {
	out, err := r.run(ctx, "docker", "inspect", "-f", "{{.State.StartedAt}}", name)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(out))
	if err != nil {
		return time.Time{}
	}
	return t
}

// Clean applies the policy and reports what it did.
//
// The order matters: containers are stopped or removed before their network, or
// the network removal fails with endpoints still attached.
func (r *Runner) Clean(ctx context.Context, p Policy, hostFor func(id string) string) ([]Action, error) {
	inv, err := r.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	seen := r.LastSeen()
	now := time.Now().UTC()

	// Decide per workspace, so a workspace's containers and network agree.
	type decision struct {
		verb, reason string
	}
	decided := map[string]decision{}
	for _, res := range inv {
		if _, done := decided[res.Workspace]; done {
			continue
		}
		switch {
		case p.Known != nil && !p.Known[res.Workspace]:
			decided[res.Workspace] = decision{"remove", "workspace no longer exists"}
			continue
		}
		last, has := seen[hostFor(res.Workspace)]
		if !has || last.IsZero() {
			// Never routed, or no request yet. Fall back to start time, which
			// answers "how long has this existed" rather than "how long since
			// anyone used it" - so say so rather than implying more.
			last = res.Started
		}
		idle := now.Sub(last)
		switch {
		case last.IsZero():
			decided[res.Workspace] = decision{"keep", "no age information"}
		case p.RemoveIdle > 0 && idle > p.RemoveIdle:
			decided[res.Workspace] = decision{"remove", fmt.Sprintf("idle %s", round(idle))}
		case p.StopIdle > 0 && idle > p.StopIdle:
			decided[res.Workspace] = decision{"stop", fmt.Sprintf("idle %s", round(idle))}
		default:
			decided[res.Workspace] = decision{"keep", fmt.Sprintf("used %s ago", round(idle))}
		}
	}

	var actions []Action
	// Containers first, then networks.
	for _, kind := range []string{"container", "network"} {
		for _, res := range inv {
			if res.Kind != kind {
				continue
			}
			d := decided[res.Workspace]
			verb := d.verb
			if res.Kind == "network" && verb == "stop" {
				// Stopping frees memory; the network costs nothing and the
				// workspace is expected to come back.
				verb = "keep"
			}
			if verb == "stop" && !res.Running {
				verb = "keep"
			}
			actions = append(actions, Action{res, verb, d.reason})
			if p.DryRun || verb == "keep" {
				continue
			}
			if err := r.apply(ctx, res, verb); err != nil {
				return actions, err
			}
		}
	}
	return actions, nil
}

func (r *Runner) apply(ctx context.Context, res Resource, verb string) error {
	switch {
	case res.Kind == "container" && verb == "stop":
		_, err := r.run(ctx, "docker", "stop", "-t", "5", res.Name)
		return err
	case res.Kind == "container" && verb == "remove":
		_, err := r.run(ctx, "docker", "rm", "-f", "-v", res.Name)
		return err
	case res.Kind == "network" && verb == "remove":
		r.DetachProxy(ctx, res.Name)
		_, _ = r.run(ctx, "docker", "network", "rm", res.Name)
		_, _ = r.RemoveRoutes(strings.TrimPrefix(res.Name, workspaceNetwork))
		return nil
	}
	return nil
}

func round(d time.Duration) time.Duration {
	if d > time.Hour {
		return d.Round(time.Minute)
	}
	return d.Round(time.Second)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
