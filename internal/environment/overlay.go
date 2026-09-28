// Package environment is the environment layer: containers, networks and
// routing. It never calls git.
//
// Its whole input is a workspace ID and a directory, which is the narrow
// boundary described in vision.md. That is what keeps it usable with jj, plain
// directories and CI checkouts.
package environment

import (
	"fmt"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"go.yaml.in/yaml/v3"
)

// Network names are chosen so a workspace network sorts BEFORE the baseline's.
//
// Docker resolves a name across a container's attached networks in alphabetical
// order of network name and returns the first match, so this ordering is what
// gives an overlay fall-through: its own changed services answer, and everything
// else falls through to the baseline. Measured in spikes/a-networking.md, and
// guarded by spikes/a3-name-order.sh.
//
// The numeric prefixes are deliberate. The ordering must be obvious to a reader
// rather than an ASCII-table fact about whether "-" precedes "_".
const (
	BaselineNetwork  = "bopper_90_baseline"
	workspaceNetwork = "bopper_10_ws_"
	BaselineProject  = "bopper-baseline"
	workspaceProject = "bopper-ws-"
)

// WorkspaceNetwork is the network name for one workspace.
func WorkspaceNetwork(id string) string { return workspaceNetwork + id }

// WorkspaceProject is the Compose project name for one workspace.
func WorkspaceProject(id string) string { return workspaceProject + id }

// OverlayName is the Compose service key an overlay uses for a baseline service.
//
// The rename is load-bearing, not cosmetic. Compose adds the service key as a
// network alias on every network the service joins. If the overlay kept the key
// "orders", it would answer to "orders" on the baseline network too, and Docker
// would round-robin the baseline's own traffic between the two containers -
// measured at a clean 10/10 split in spikes/a-networking.md, with no error and
// no log line. Renaming the key keeps the baseline network free of the plain
// name; the plain name is added back as an explicit alias on the workspace
// network only.
func OverlayName(service, id string) string { return service + "--" + id }

// BaselineOverride is a Compose fragment that renames the baseline project's
// default network, so overlays can attach to a known, correctly sorting name.
func BaselineOverride() ([]byte, error) {
	return yaml.Marshal(map[string]any{
		"networks": map[string]any{
			"default": map[string]any{"name": BaselineNetwork},
		},
	})
}

// RouteOptions describes how an overlay service should be exposed.
type RouteOptions struct {
	Host string // e.g. "feature-x.localhost"
	Port int    // container port to route to; 0 disables routing
}

// BuildOverlay renders the Compose file for a workspace's overlay.
//
// Only the named services are included. Everything else is deliberately absent:
// an unchanged service must fall through to the baseline, and a queue consumer
// or scheduled job must not run a second copy that competes with the baseline's.
func BuildOverlay(project *types.Project, id string, services []string, routes map[string]RouteOptions) ([]byte, error) {
	if len(services) == 0 {
		return nil, fmt.Errorf("no services to overlay")
	}
	sorted := append([]string(nil), services...)
	sort.Strings(sorted)

	inOverlay := map[string]bool{}
	for _, s := range sorted {
		inOverlay[s] = true
	}

	out := map[string]any{}
	svcs := map[string]any{}

	for _, name := range sorted {
		svc, ok := project.Services[name]
		if !ok {
			return nil, fmt.Errorf("service %q is not in the compose project", name)
		}
		m, err := serviceToMap(svc)
		if err != nil {
			return nil, err
		}

		// Compose derives the container name from the project and service key;
		// an explicit one would collide with the baseline's.
		delete(m, "container_name")

		// Published host ports would collide with the baseline's. The overlay is
		// reached through the shared proxy instead.
		delete(m, "ports")

		// depends_on can only refer to services that exist in this project.
		if dep, ok := m["depends_on"]; ok {
			m["depends_on"] = renameDeps(dep, id, inOverlay)
		}

		m["networks"] = map[string]any{
			// The plain name, restored on the workspace network only, so the
			// overlay's own services find each other as they normally would.
			"ws": map[string]any{"aliases": []string{name}},
			// No alias here: see OverlayName.
			"baseline": map[string]any{},
		}

		if r, ok := routes[name]; ok && r.Port > 0 && r.Host != "" {
			m["labels"] = mergeLabels(m["labels"], traefikLabels(id, name, r))
		}
		svcs[OverlayName(name, id)] = m
	}

	out["services"] = svcs
	out["networks"] = map[string]any{
		"ws": map[string]any{"name": WorkspaceNetwork(id)},
		"baseline": map[string]any{
			"name":     BaselineNetwork,
			"external": true,
		},
	}
	return yaml.Marshal(out)
}

// serviceToMap round-trips a resolved service through YAML, so the overlay
// inherits exactly what Compose resolved - build context, environment, command,
// healthcheck - rather than a hand-maintained subset that drifts.
func serviceToMap(svc types.ServiceConfig) (map[string]any, error) {
	svc.Name = ""
	b, err := yaml.Marshal(svc)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	// A zero-valued name marshals as an empty key; drop it and anything else
	// that serialized empty.
	for k, v := range m {
		if v == nil || k == "name" {
			delete(m, k)
		}
	}
	return m, nil
}

// renameDeps rewrites depends_on to the overlay's service keys, dropping any
// dependency the overlay does not run. A dropped dependency is served by the
// baseline, which is already up.
func renameDeps(dep any, id string, inOverlay map[string]bool) any {
	switch d := dep.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range d {
			if inOverlay[k] {
				out[OverlayName(k, id)] = v
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		var out []any
		for _, v := range d {
			if s, ok := v.(string); ok && inOverlay[s] {
				out = append(out, OverlayName(s, id))
			}
		}
		return out
	}
	return nil
}

func traefikLabels(id, service string, r RouteOptions) map[string]string {
	router := id + "-" + service
	return map[string]string{
		"traefik.enable": "true",
		"traefik.http.routers." + router + ".rule":                      "Host(`" + r.Host + "`)",
		"traefik.http.routers." + router + ".entrypoints":               "web",
		"traefik.http.services." + router + ".loadbalancer.server.port": fmt.Sprint(r.Port),
		"traefik.docker.network":                                        WorkspaceNetwork(id),
		"bopper.workspace":                                              id,
	}
}

// mergeLabels folds Bopper's labels into whatever the service already declared,
// accepting either Compose label form.
func mergeLabels(existing any, add map[string]string) map[string]string {
	out := map[string]string{}
	switch e := existing.(type) {
	case map[string]any:
		for k, v := range e {
			out[k] = fmt.Sprint(v)
		}
	case []any:
		for _, v := range e {
			s := fmt.Sprint(v)
			if i := strings.IndexByte(s, '='); i > 0 {
				out[s[:i]] = s[i+1:]
			} else {
				out[s] = ""
			}
		}
	}
	for k, v := range add {
		out[k] = v
	}
	return out
}
