package environment

import (
	"context"
	"fmt"

	"github.com/compose-spec/compose-go/v2/types"
)

// Target is everything the environment layer needs to know about a workspace.
//
// It is an ID and a directory, and deliberately nothing else. The environment
// layer never calls git, so it works just as well against a plain directory or
// a CI checkout as against a worktree.
type Target struct {
	ID   string
	Dir  string
	Host string
}

// Result reports what Up actually did, so the caller can tell the user.
type Result struct {
	Changed   []string // services the overlay runs
	Total     int      // services in the project
	URL       string   // where the workspace is reachable, if anything is routed
	FullStack bool     // the branch changed everything; the overlay saves nothing
	// HeaderRouting reports whether a baseline service can reach this overlay.
	// When false the changed service must sit at the edge of the call graph.
	HeaderRouting bool
}

// Up brings a workspace's environment to life: baseline, proxy, then the
// overlay containing only the changed services.
//
// The baseline is started first and left running. That is the point: every
// workspace shares it, and an unchanged service falls through to it.
func (r *Runner) Up(ctx context.Context, t Target, baselineDir string,
	project *types.Project, changed []string) (*Result, error) {

	res := &Result{Changed: changed, Total: len(project.Services)}
	if len(changed) == 0 {
		// Nothing to overlay. The baseline serves the whole stack, which is the
		// best case rather than a failure.
		if err := r.startBaseline(ctx, baselineDir, project); err != nil {
			return nil, err
		}
		return res, nil
	}
	res.FullStack = len(changed) >= len(project.Services)

	if err := r.startBaseline(ctx, baselineDir, project); err != nil {
		return nil, fmt.Errorf("starting the baseline: %w", err)
	}
	if err := r.EnsureProxy(ctx); err != nil {
		return nil, err
	}
	if err := r.EnsureNetwork(ctx, WorkspaceNetwork(t.ID)); err != nil {
		return nil, err
	}

	routes := RoutesFor(project, changed, t.Host)
	if err := r.OverlayUp(ctx, t.ID, t.Dir, project, changed, routes); err != nil {
		return nil, fmt.Errorf("starting the overlay: %w", err)
	}
	if err := r.AttachProxy(ctx, WorkspaceNetwork(t.ID)); err != nil {
		return nil, err
	}
	// Routes are written after the containers exist, so the proxy never has a
	// rule pointing at a name that does not resolve yet.
	routesChanged, err := r.WriteRoutes(t.ID, routes)
	if err != nil {
		return nil, err
	}
	// Record what this overlay runs, then rebuild the shared intercept routers
	// from every workspace at once.
	if err := r.SetOverlay(t.ID, changed); err != nil {
		return nil, err
	}
	interceptChanged, err := r.syncIntercepts(ctx)
	if err != nil {
		return nil, err
	}
	if routesChanged || interceptChanged {
		if err := r.ReloadProxy(ctx); err != nil {
			return nil, err
		}
	}
	if len(routes) > 0 {
		res.URL = URL(t.Host)
	}
	res.HeaderRouting = r.HeaderRouting()
	return res, nil
}

// startBaseline brings the baseline up in whichever mode is configured.
func (r *Runner) startBaseline(ctx context.Context, dir string, project *types.Project) error {
	if !r.HeaderRouting() {
		return r.BaselineUp(ctx, dir, nil)
	}
	doc, err := BuildBaselineHeaderRouted(project)
	if err != nil {
		return err
	}
	return r.BaselineUpHeaderRouted(ctx, dir, doc)
}

// syncIntercepts rewrites the shared intercept routers and gives the proxy the
// plain service names. Off by default, when it is a no-op.
func (r *Runner) syncIntercepts(ctx context.Context) (bool, error) {
	if !r.HeaderRouting() {
		changed, err := r.WriteInterceptRoutes(nil, nil)
		return changed, err
	}
	services := r.InterceptedServices()
	changed, err := r.WriteInterceptRoutes(services, r.Overlays())
	if err != nil {
		return false, err
	}
	if err := r.ProxyAliases(ctx, services); err != nil {
		return changed, err
	}
	return changed, nil
}

// Down removes a workspace's environment. The baseline is left alone: other
// workspaces are still falling through to it.
func (r *Runner) Down(ctx context.Context, t Target) error {
	removed, _ := r.RemoveRoutes(t.ID)
	_ = r.SetOverlay(t.ID, nil)
	interceptChanged, _ := r.syncIntercepts(ctx)
	if removed || interceptChanged {
		_ = r.ReloadProxy(ctx)
	}
	r.DetachProxy(ctx, WorkspaceNetwork(t.ID))
	if err := r.OverlayDown(ctx, t.ID, t.Dir); err != nil {
		return err
	}
	_, _ = r.run(ctx, "docker", "network", "rm", WorkspaceNetwork(t.ID))
	return nil
}
