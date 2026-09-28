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
		if err := r.BaselineUp(ctx, baselineDir, nil); err != nil {
			return nil, err
		}
		return res, nil
	}
	res.FullStack = len(changed) >= len(project.Services)

	if err := r.BaselineUp(ctx, baselineDir, nil); err != nil {
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
	if routesChanged {
		if err := r.ReloadProxy(ctx); err != nil {
			return nil, err
		}
	}
	if len(routes) > 0 {
		res.URL = URL(t.Host)
	}
	return res, nil
}

// Down removes a workspace's environment. The baseline is left alone: other
// workspaces are still falling through to it.
func (r *Runner) Down(ctx context.Context, t Target) error {
	if removed, _ := r.RemoveRoutes(t.ID); removed {
		_ = r.ReloadProxy(ctx)
	}
	r.DetachProxy(ctx, WorkspaceNetwork(t.ID))
	if err := r.OverlayDown(ctx, t.ID, t.Dir); err != nil {
		return err
	}
	_, _ = r.run(ctx, "docker", "network", "rm", WorkspaceNetwork(t.ID))
	return nil
}
