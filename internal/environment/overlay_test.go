package environment

import (
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"go.yaml.in/yaml/v3"
)

func project() *types.Project {
	return &types.Project{Services: types.Services{
		"orders": {
			Name:          "orders",
			ContainerName: "shop-orders",
			Build:         &types.BuildConfig{Context: "/repo/orders"},
			Ports:         []types.ServicePortConfig{{Target: 80, Published: "8091"}},
			DependsOn:     types.DependsOnConfig{"db": {Condition: "service_started"}},
		},
		"payments": {Name: "payments", Build: &types.BuildConfig{Context: "/repo/payments"}},
		"db":       {Name: "db", Image: "postgres:16-alpine"},
	}}
}

func build(t *testing.T, services []string, routes map[string]RouteOptions) map[string]any {
	t.Helper()
	doc, err := BuildOverlay(project(), "feat", services, routes)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(doc, &m); err != nil {
		t.Fatalf("generated invalid yaml: %v\n%s", err, doc)
	}
	return m
}

func svc(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	all, ok := m["services"].(map[string]any)
	if !ok {
		t.Fatalf("no services in %v", m)
	}
	s, ok := all[key].(map[string]any)
	if !ok {
		t.Fatalf("no service %q; got %v", key, keys(all))
	}
	return s
}

// The rename is what keeps the baseline safe. Compose adds the service key as a
// network alias on every attached network, so a key of "orders" would answer to
// "orders" on the baseline network too and split the baseline's own traffic.
func TestServiceKeyIsRenamedAndPlainNameOnlyOnWorkspaceNetwork(t *testing.T) {
	m := build(t, []string{"orders"}, nil)
	s := svc(t, m, "orders--feat")

	nets := s["networks"].(map[string]any)
	ws := nets["ws"].(map[string]any)
	aliases, _ := ws["aliases"].([]any)
	if len(aliases) != 1 || aliases[0] != "orders" {
		t.Fatalf("workspace network aliases = %v, want [orders]", aliases)
	}
	baseline, ok := nets["baseline"].(map[string]any)
	if !ok && nets["baseline"] != nil {
		t.Fatalf("baseline network entry = %v", nets["baseline"])
	}
	if len(baseline) != 0 {
		t.Fatalf("baseline network must carry NO aliases, got %v", baseline)
	}
}

// Published ports and container names both collide with the baseline's.
func TestCollidingFieldsAreStripped(t *testing.T) {
	s := svc(t, build(t, []string{"orders"}, nil), "orders--feat")
	if _, ok := s["ports"]; ok {
		t.Error("published ports survived; they would collide with the baseline")
	}
	if _, ok := s["container_name"]; ok {
		t.Error("container_name survived; it would collide with the baseline")
	}
	if s["build"] == nil {
		t.Error("build config was lost")
	}
}

// A dependency the overlay does not run is already served by the baseline. If
// every dependency drops, the key must be REMOVED, not set to null: compose
// rejects a null depends_on outright.
func TestDependsOnDropsBaselineServedDependencies(t *testing.T) {
	s := svc(t, build(t, []string{"orders"}, nil), "orders--feat")
	if v, present := s["depends_on"]; present {
		t.Fatalf("depends_on should be absent, got %#v", v)
	}
}

func TestDependsOnIsRenamedWhenBothServicesAreInTheOverlay(t *testing.T) {
	p := project()
	p.Services["orders"] = types.ServiceConfig{
		Name:      "orders",
		Build:     &types.BuildConfig{Context: "/repo/orders"},
		DependsOn: types.DependsOnConfig{"payments": {Condition: "service_started"}},
	}
	doc, err := BuildOverlay(p, "feat", []string{"orders", "payments"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(doc, &m); err != nil {
		t.Fatal(err)
	}
	dep := svc(t, m, "orders--feat")["depends_on"]
	d, ok := dep.(map[string]any)
	if !ok {
		t.Fatalf("depends_on = %#v", dep)
	}
	if _, ok := d["payments--feat"]; !ok {
		t.Fatalf("depends_on was not renamed: %v", keys(d))
	}
}

// Both networks are external: Bopper owns their lifecycle, and the workspace
// network must outlive a compose down so the proxy's attachment survives.
func TestBothNetworksAreExternal(t *testing.T) {
	m := build(t, []string{"orders"}, nil)
	nets := m["networks"].(map[string]any)
	for name, want := range map[string]string{"ws": WorkspaceNetwork("feat"), "baseline": BaselineNetwork} {
		n := nets[name].(map[string]any)
		if n["name"] != want {
			t.Errorf("%s network name = %v, want %v", name, n["name"], want)
		}
		if n["external"] != true {
			t.Errorf("%s network must be external", name)
		}
	}
}

func TestOnlyRequestedServicesAppear(t *testing.T) {
	m := build(t, []string{"orders"}, nil)
	all := m["services"].(map[string]any)
	if len(all) != 1 {
		t.Fatalf("overlay contains %v, want only orders--feat", keys(all))
	}
}

func TestEmptyServiceListIsAnError(t *testing.T) {
	if _, err := BuildOverlay(project(), "feat", nil, nil); err == nil {
		t.Fatal("expected an error for an empty service list")
	}
}

func TestWorkspaceLabelIsSetForCleanup(t *testing.T) {
	s := svc(t, build(t, []string{"orders"}, nil), "orders--feat")
	labels, ok := s["labels"].(map[string]any)
	if !ok {
		t.Fatalf("labels = %#v", s["labels"])
	}
	if labels["bopper.workspace"] != "feat" {
		t.Fatalf("bopper.workspace = %v, want feat", labels["bopper.workspace"])
	}
}

// RoutesFor picks the port a developer would otherwise reach directly.
func TestRoutesForUsesPublishedThenExposedPorts(t *testing.T) {
	p := project()
	p.Services["payments"] = types.ServiceConfig{
		Name: "payments", Expose: []string{"9000"},
		Build: &types.BuildConfig{Context: "/repo/payments"},
	}
	routes := RoutesFor(p, []string{"orders", "payments", "db"}, "feat.localhost")
	if routes["orders"].Port != 80 {
		t.Errorf("orders port = %d, want 80 (the container port, not the published one)", routes["orders"].Port)
	}
	if routes["payments"].Port != 9000 {
		t.Errorf("payments port = %d, want 9000 from expose", routes["payments"].Port)
	}
	if _, ok := routes["db"]; ok {
		t.Error("db publishes nothing and should not be routed")
	}
}

func TestBaselineOverrideRenamesTheDefaultNetwork(t *testing.T) {
	doc, err := BaselineOverride()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), BaselineNetwork) {
		t.Fatalf("override does not name the baseline network:\n%s", doc)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
