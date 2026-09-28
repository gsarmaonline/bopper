package environment

import (
	"os"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"go.yaml.in/yaml/v3"
)

func runner(t *testing.T) *Runner {
	t.Helper()
	return &Runner{StateDir: t.TempDir()}
}

func loadYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid yaml: %v\n%s", err, b)
	}
	return m
}

func routers(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	http, ok := m["http"].(map[string]any)
	if !ok {
		t.Fatalf("no http section: %v", m)
	}
	r, _ := http["routers"].(map[string]any)
	return r
}

// Router names share one namespace across every file the provider loads. The
// edge router and the intercept router were both "<id>-<service>", so the
// intercept file silently overwrote the edge router and every request to
// <id>.localhost 404'd while header-routed calls kept working.
func TestEdgeAndInterceptRouterNamesDoNotCollide(t *testing.T) {
	r := runner(t)
	if _, err := r.WriteRoutes("feat", map[string]RouteOptions{
		"orders": {Host: "feat.localhost", Port: 80},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteInterceptRoutes([]string{"orders"},
		map[string][]string{"feat": {"orders"}}); err != nil {
		t.Fatal(err)
	}

	edge := routers(t, loadYAML(t, r.dynamicDir()+"/feat.yaml"))
	intercept := routers(t, loadYAML(t, r.dynamicDir()+"/00-intercept.yaml"))
	for name := range edge {
		if _, clash := intercept[name]; clash {
			t.Fatalf("router %q is defined in both files; one will overwrite the other", name)
		}
	}
	if len(edge) == 0 || len(intercept) == 0 {
		t.Fatal("expected routers in both files")
	}
}

// A tagged request must never fall through to the baseline router because a
// rule happened to be written longer.
func TestTaggedRouterOutranksTheBaselineRouter(t *testing.T) {
	r := runner(t)
	if _, err := r.WriteInterceptRoutes([]string{"orders"},
		map[string][]string{"feat": {"orders"}}); err != nil {
		t.Fatal(err)
	}
	rs := routers(t, loadYAML(t, r.dynamicDir()+"/00-intercept.yaml"))

	tagged, ok := rs["hdr-feat-orders"].(map[string]any)
	if !ok {
		t.Fatalf("no tagged router; got %v", keys(rs))
	}
	base, ok := rs["hdr-base-orders"].(map[string]any)
	if !ok {
		t.Fatalf("no baseline router; got %v", keys(rs))
	}
	tp, _ := tagged["priority"].(int)
	bp, _ := base["priority"].(int)
	if tp <= bp {
		t.Fatalf("tagged priority %d must beat baseline priority %d", tp, bp)
	}
	if !strings.Contains(tagged["rule"].(string), Header) {
		t.Fatalf("tagged rule does not match the header: %v", tagged["rule"])
	}
}

// A workspace only intercepts what it changed; everything else keeps falling
// through to the baseline directly.
func TestOnlyOverriddenServicesAreIntercepted(t *testing.T) {
	r := runner(t)
	if _, err := r.WriteInterceptRoutes([]string{"orders", "payments"},
		map[string][]string{"feat": {"orders"}}); err != nil {
		t.Fatal(err)
	}
	rs := routers(t, loadYAML(t, r.dynamicDir()+"/00-intercept.yaml"))
	if _, ok := rs["hdr-feat-orders"]; !ok {
		t.Error("orders is overridden and should be intercepted for feat")
	}
	if _, ok := rs["hdr-feat-payments"]; ok {
		t.Error("payments is not overridden and must not be intercepted for feat")
	}
	if _, ok := rs["hdr-base-payments"]; !ok {
		t.Error("payments still needs its baseline router")
	}
}

// The edge tags requests, so a hostname is the whole interface: no browser
// extension, no curl flag.
func TestEdgeInjectsTheWorkspaceHeader(t *testing.T) {
	r := runner(t)
	if _, err := r.WriteRoutes("feat", map[string]RouteOptions{
		"orders": {Host: "feat.localhost", Port: 80},
	}); err != nil {
		t.Fatal(err)
	}
	m := loadYAML(t, r.dynamicDir()+"/feat.yaml")
	http := m["http"].(map[string]any)
	mw, ok := http["middlewares"].(map[string]any)
	if !ok {
		t.Fatalf("no middlewares: %v", keys(http))
	}
	tag, ok := mw["tag-feat"].(map[string]any)
	if !ok {
		t.Fatalf("no tag middleware: %v", keys(mw))
	}
	hdrs := tag["headers"].(map[string]any)["customRequestHeaders"].(map[string]any)
	if hdrs[Header] != "feat" {
		t.Fatalf("%s = %v, want feat", Header, hdrs[Header])
	}
	for _, rt := range routers(t, m) {
		used := rt.(map[string]any)["middlewares"]
		if used == nil {
			t.Fatal("edge router does not apply the tag middleware")
		}
	}
}

// Under header routing the baseline gives up its plain aliases, for the same
// reason the overlay does: two containers on one alias split traffic at random.
func TestHeaderRoutedBaselineGivesUpPlainAliases(t *testing.T) {
	p := &types.Project{Services: types.Services{
		"orders": {Name: "orders", Image: "x", ContainerName: "shop-orders",
			DependsOn: types.DependsOnConfig{"db": {Condition: "service_started"}}},
		"db": {Name: "db", Image: "postgres"},
	}}
	doc, err := BuildBaselineHeaderRouted(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(doc, &m); err != nil {
		t.Fatal(err)
	}
	svcs := m["services"].(map[string]any)
	s, ok := svcs["orders--base"].(map[string]any)
	if !ok {
		t.Fatalf("service key was not renamed; got %v", keys(svcs))
	}
	nets := s["networks"].(map[string]any)
	if len(nets["baseline"].(map[string]any)) != 0 {
		t.Fatalf("baseline service must carry no aliases, got %v", nets["baseline"])
	}
	if _, ok := s["container_name"]; ok {
		t.Error("container_name should be dropped")
	}
	dep := s["depends_on"].(map[string]any)
	if _, ok := dep["db--base"]; !ok {
		t.Fatalf("depends_on was not renamed: %v", keys(dep))
	}
}

func TestOverlayStateRoundTrips(t *testing.T) {
	r := runner(t)
	if err := r.SetOverlay("feat", []string{"orders"}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetOverlay("beta", []string{"payments", "orders"}); err != nil {
		t.Fatal(err)
	}
	if got := r.InterceptedServices(); len(got) != 2 || got[0] != "orders" || got[1] != "payments" {
		t.Fatalf("intercepted = %v", got)
	}
	if err := r.SetOverlay("beta", nil); err != nil {
		t.Fatal(err)
	}
	if got := r.InterceptedServices(); len(got) != 1 || got[0] != "orders" {
		t.Fatalf("after removing beta, intercepted = %v", got)
	}
}

func TestHeaderRoutingIsOffByDefault(t *testing.T) {
	r := runner(t)
	if r.HeaderRouting() {
		t.Fatal("header routing must be opt-in")
	}
	if err := r.SetHeaderRouting(true); err != nil {
		t.Fatal(err)
	}
	if !r.HeaderRouting() {
		t.Fatal("header routing did not turn on")
	}
	if err := r.SetHeaderRouting(false); err != nil {
		t.Fatal(err)
	}
	if r.HeaderRouting() {
		t.Fatal("header routing did not turn off")
	}
}

// Writing the same routes twice must not report a change, or every bop up would
// restart the shared proxy and interrupt every other workspace.
func TestUnchangedRoutesReportNoChange(t *testing.T) {
	r := runner(t)
	routes := map[string]RouteOptions{"orders": {Host: "feat.localhost", Port: 80}}
	if changed, _ := r.WriteRoutes("feat", routes); !changed {
		t.Fatal("first write should report a change")
	}
	if changed, _ := r.WriteRoutes("feat", routes); changed {
		t.Fatal("identical write must not report a change")
	}
}
