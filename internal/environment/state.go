package environment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Header routing needs to know every workspace's overlay at once, because the
// intercept routers are one shared file: a router for service "orders" has to
// list every workspace that overrides it. Up and Down each see only one
// workspace, so the set is recorded here.
const overlayStateFile = "overlays.json"

func (r *Runner) statePath() string { return filepath.Join(r.StateDir, overlayStateFile) }

// Overlays returns the services each workspace's overlay currently runs.
func (r *Runner) Overlays() map[string][]string {
	out := map[string][]string{}
	b, err := os.ReadFile(r.statePath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

// SetOverlay records, or with no services forgets, one workspace's overlay.
func (r *Runner) SetOverlay(id string, services []string) error {
	all := r.Overlays()
	if len(services) == 0 {
		delete(all, id)
	} else {
		s := append([]string(nil), services...)
		sort.Strings(s)
		all[id] = s
	}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.statePath(), append(b, '\n'), 0o644)
}

// InterceptedServices is every service any workspace currently overrides. Only
// these need the proxy in front of them; the rest are reached directly, which
// keeps the proxy out of the path of calls no workspace has changed.
func (r *Runner) InterceptedServices() []string {
	seen := map[string]bool{}
	for _, svcs := range r.Overlays() {
		for _, s := range svcs {
			seen[s] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
