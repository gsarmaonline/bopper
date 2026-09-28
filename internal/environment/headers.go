package environment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"
)

// Header is the tag a request carries to say which workspace it belongs to.
const Header = "X-Worktree"

// Header routing lets a BASELINE service call an OVERLAY service. Without it a
// changed service must sit at the edge of the call graph, because a baseline
// container resolves every name to another baseline container.
//
// It is opt-in, and it has to be. Turning it on changes the baseline: its
// services give up their plain network aliases so the proxy can hold them and
// route by header, which means every internal call in the baseline crosses the
// proxy. That is a real cost in latency and a single point of failure, and it
// buys nothing for a branch whose changed service is already at the edge.
//
// It also depends on something Bopper cannot provide. The proxy tags a request
// at the edge, but a baseline service that does not FORWARD the header sends
// the next hop to the baseline version. Propagation is the application's job -
// OpenTelemetry baggage, or explicit forwarding - and where it is absent, the
// no-routing mode stays the honest choice.

const headerModeFile = "header-routing"

// HeaderRouting reports whether header routing is enabled for this installation.
func (r *Runner) HeaderRouting() bool {
	_, err := os.Stat(filepath.Join(r.StateDir, headerModeFile))
	return err == nil
}

// SetHeaderRouting turns header routing on or off. The caller must restart the
// baseline afterwards: the mode changes how its services are named.
func (r *Runner) SetHeaderRouting(on bool) error {
	path := filepath.Join(r.StateDir, headerModeFile)
	if !on {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(path, []byte("on\n"), 0o644)
}

// WriteInterceptRoutes publishes the routers that put the proxy in front of the
// baseline's services.
//
// For each service there are two routers. The tagged one matches a workspace's
// header and sends the request to that workspace's overlay; the untagged one
// catches everything else and sends it to the baseline. The tagged router is
// given the higher priority explicitly rather than relying on Traefik's
// rule-length default, which is easy to perturb by renaming a service.
//
// workspaces maps a workspace ID to the services its overlay actually runs. A
// workspace only intercepts the services it changed; everything else keeps
// falling through to the baseline.
func (r *Runner) WriteInterceptRoutes(services []string, workspaces map[string][]string) (bool, error) {
	dir := r.dynamicDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	path := filepath.Join(dir, "00-intercept.yaml")

	if len(services) == 0 {
		if _, err := os.Stat(path); err == nil {
			return true, os.Remove(path)
		}
		return false, nil
	}

	routers := map[string]any{}
	svcDefs := map[string]any{}

	ids := make([]string, 0, len(workspaces))
	for id := range workspaces {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, name := range services {
		// See WriteRoutes: router names are global to the file provider, so
		// these carry their own prefixes.
		base := "hdr-base-" + name
		routers[base] = map[string]any{
			"rule":        fmt.Sprintf("Host(`%s`)", name),
			"service":     base,
			"entryPoints": []string{"web"},
			"priority":    10,
		}
		svcDefs[base] = loadBalancer(BaselineName(name), 80)

		for _, id := range ids {
			if !contains(workspaces[id], name) {
				continue
			}
			key := "hdr-" + id + "-" + name
			routers[key] = map[string]any{
				"rule": fmt.Sprintf("Host(`%s`) && Header(`%s`, `%s`)", name, Header, id),
				// A tagged request must never fall to the baseline router just
				// because a rule happened to be written longer.
				"priority":    100,
				"service":     key,
				"entryPoints": []string{"web"},
			}
			svcDefs[key] = loadBalancer(OverlayName(name, id), 80)
		}
	}

	doc, err := yaml.Marshal(map[string]any{
		"http": map[string]any{"routers": routers, "services": svcDefs},
	})
	if err != nil {
		return false, err
	}
	if old, err := os.ReadFile(path); err == nil && string(old) == string(doc) {
		return false, nil
	}
	return true, os.WriteFile(path, doc, 0o644)
}

func loadBalancer(host string, port int) map[string]any {
	return map[string]any{
		"loadBalancer": map[string]any{
			"servers": []any{map[string]any{"url": fmt.Sprintf("http://%s:%d", host, port)}},
		},
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// BaselineUpHeaderRouted starts the baseline with its services renamed, so the
// proxy can hold their plain names.
func (r *Runner) BaselineUpHeaderRouted(ctx context.Context, dir string, doc []byte) error {
	path := filepath.Join(r.StateDir, "baseline-headers.yaml")
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		return err
	}
	if err := r.EnsureNetwork(ctx, BaselineNetwork); err != nil {
		return err
	}
	_, err := r.run(ctx, "docker", "compose", "--project-directory", dir,
		"-f", path, "-p", BaselineProject, "up", "-d", "--build", "--wait")
	return err
}

// ProxyAliases gives the proxy the plain service names on the baseline network.
//
// Docker only accepts network aliases when a container JOINS a network, so this
// reconnects the proxy rather than editing it in place.
func (r *Runner) ProxyAliases(ctx context.Context, services []string) error {
	_, _ = r.run(ctx, "docker", "network", "disconnect", "-f", BaselineNetwork, ProxyName)
	args := []string{"network", "connect"}
	for _, s := range services {
		args = append(args, "--alias", s)
	}
	args = append(args, BaselineNetwork, ProxyName)
	_, err := r.run(ctx, "docker", args...)
	return err
}
