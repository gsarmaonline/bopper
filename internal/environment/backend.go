package environment

import (
	"context"

	"github.com/gsarmaonline/bopper/internal/stack"
)

// Backend runs a workspace's environment.
//
// Compose is the first backend, and today the only one. The interface exists so
// that stays a fact about the implementation rather than about Bopper: nothing
// above this line mentions Compose, and a second backend replaces one type
// rather than threading new concepts through the CLI, the data layer and the
// workspace layer.
//
// The boundary that makes this possible is already in the design: the workspace
// layer hands over a workspace ID and a directory, and nothing else. That is why
// a backend can be swapped without touching git, reflinks or change detection's
// meaning - see vision.md, "Architecture: two layers with a narrow boundary".
//
// What a second backend must provide is exactly the methods below. Routing and
// lifecycle are part of the contract because every backend needs an answer to
// them, not because Traefik and Docker labels are the only answers; a backend
// that cannot honour one should say so rather than pretend.
type Backend interface {
	// Name identifies the backend in output and errors.
	Name() string

	// Available reports whether this backend can run here at all.
	Available(ctx context.Context) error

	// Describe reads the application declared in dir.
	Describe(ctx context.Context, dir string) (stack.Stack, error)

	// Changed reports which services a workspace changes relative to the
	// baseline. What counts as a change is the backend's to define; for
	// Compose it is a hash of each service's build inputs and resolved
	// configuration.
	Changed(ctx context.Context, baselineDir, wsDir string) ([]string, error)

	// EnsureBaseline brings up the one shared stack every workspace falls
	// through to. It is idempotent.
	EnsureBaseline(ctx context.Context, dir string) error

	// BaselineRunning reports whether that shared stack is up.
	BaselineRunning(ctx context.Context) bool

	// Up starts only the named services for one workspace, and routes to them.
	Up(ctx context.Context, t Target, baselineDir string, changed []string) (*Result, error)

	// Down removes a workspace's environment, leaving the baseline alone.
	Down(ctx context.Context, t Target) error

	// Containers lists what is running for a workspace.
	Containers(ctx context.Context, id string) ([]string, error)

	// ServiceContainer resolves a baseline service to something Exec can target.
	ServiceContainer(ctx context.Context, service string) (string, error)

	// Exec runs a command inside a container with env set on the process. The
	// data layer uses this to reach a database without knowing how the backend
	// runs it.
	Exec(ctx context.Context, container string, env []string, args ...string) (string, error)

	// WaitRunning blocks until a container is running.
	WaitRunning(ctx context.Context, container string, attempts int) error

	// Reclaim applies a cleanup policy and reports what it did.
	Reclaim(ctx context.Context, p Policy, hostFor func(id string) string) ([]Action, error)

	// HeaderRouting reports whether a baseline service can reach an overlay.
	HeaderRouting() bool

	// SetHeaderRouting turns that on or off. The caller restarts the baseline.
	SetHeaderRouting(on bool) error
}

// Open returns the backend for this project.
//
// There is one today, so this is a constructor with a promise rather than a
// registry with a single entry. When a second backend exists, this is where it
// gets chosen - from the files in the directory, or an explicit setting - and
// nothing that calls it has to change.
func Open() (Backend, error) {
	return NewRunner()
}

// Compile-time proof that the Compose backend satisfies the contract. If a
// method drifts, this fails at build time rather than at the call site.
var _ Backend = (*Runner)(nil)
