package environment

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

// Runner drives docker compose and the Docker CLI.
//
// Bopper targets the Compose spec and the Docker API rather than Docker
// internals, which is what keeps Podman viable through its Docker-compatible
// socket. Shelling out to the compose plugin also means the user's own compose
// version resolves the file, so Bopper cannot disagree with `docker compose`
// about what the stack is.
type Runner struct {
	// StateDir holds generated compose files. They are derived artifacts,
	// rewritten on every run, and deliberately kept out of the working tree so
	// they never appear as untracked files or get cloned into a workspace.
	StateDir string
	Verbose  bool
}

// NewRunner prepares a runner with a per-user state directory.
func NewRunner() (*Runner, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(cache, "bopper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Runner{StateDir: dir}, nil
}

// Available reports whether a Docker daemon is reachable. Bopper's workspace
// layer works without one, so this is a question rather than a requirement.
func Available(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker is not available: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (r *Runner) run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if r.Verbose {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return "", cmd.Run()
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err,
			strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// BaselineUp starts the baseline stack, renaming its default network so
// overlays can attach to a known, correctly sorting name.
//
// It is idempotent: compose reconciles, so calling it when the baseline is
// already running is a cheap no-op.
func (r *Runner) BaselineUp(ctx context.Context, dir string, composeFiles []string) error {
	override, err := BaselineOverride()
	if err != nil {
		return err
	}
	path := filepath.Join(r.StateDir, "baseline-override.yaml")
	if err := os.WriteFile(path, override, 0o644); err != nil {
		return err
	}

	// Passing any -f suppresses compose's own file discovery, so the project's
	// file has to be named explicitly alongside the override.
	files := resolveFiles(dir, composeFiles)
	if len(files) == 0 {
		found, err := FindComposeFile(dir)
		if err != nil {
			return err
		}
		files = []string{found}
	}
	args := []string{"compose", "--project-directory", dir}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	args = append(args, "-f", path, "-p", BaselineProject, "up", "-d", "--wait")
	_, err = r.run(ctx, "docker", args...)
	return err
}

// BaselineRunning reports whether the baseline project has running containers.
func (r *Runner) BaselineRunning(ctx context.Context) bool {
	out, err := r.run(ctx, "docker", "ps", "-q",
		"--filter", "label=com.docker.compose.project="+BaselineProject)
	return err == nil && out != ""
}

// OverlayUp starts only the named services for one workspace.
func (r *Runner) OverlayUp(ctx context.Context, id, dir string, project *types.Project,
	services []string, routes map[string]RouteOptions) error {

	doc, err := BuildOverlay(project, id, services, routes)
	if err != nil {
		return err
	}
	path := filepath.Join(r.StateDir, "overlay-"+id+".yaml")
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		return err
	}
	_, err = r.run(ctx, "docker", "compose", "--project-directory", dir,
		"-f", path, "-p", WorkspaceProject(id), "up", "-d", "--build", "--wait")
	return err
}

// OverlayDown stops a workspace's containers and removes its network.
//
// The generated compose file is kept until the teardown succeeds; compose needs
// it to know what to remove.
func (r *Runner) OverlayDown(ctx context.Context, id, dir string) error {
	path := filepath.Join(r.StateDir, "overlay-"+id+".yaml")
	if _, err := os.Stat(path); err != nil {
		// Nothing was ever started for this workspace.
		return nil
	}
	if _, err := r.run(ctx, "docker", "compose", "--project-directory", dir,
		"-f", path, "-p", WorkspaceProject(id), "down", "-v", "--remove-orphans"); err != nil {
		return err
	}
	return os.Remove(path)
}

// OverlayContainers lists the running containers for one workspace.
func (r *Runner) OverlayContainers(ctx context.Context, id string) ([]string, error) {
	out, err := r.run(ctx, "docker", "ps", "--format", "{{.Names}}",
		"--filter", "label=com.docker.compose.project="+WorkspaceProject(id))
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// ComposeNames is the discovery order docker compose itself uses.
var ComposeNames = []string{
	"compose.yaml", "compose.yml",
	"docker-compose.yaml", "docker-compose.yml",
}

// FindComposeFile locates the project's compose file in dir.
func FindComposeFile(dir string) (string, error) {
	for _, n := range ComposeNames {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no compose file in %s", dir)
}

// resolveFiles makes compose file paths absolute against dir.
func resolveFiles(dir string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if !filepath.IsAbs(f) {
			f = filepath.Join(dir, f)
		}
		out = append(out, f)
	}
	return out
}

// RoutesFor decides which services are worth exposing through the proxy.
//
// A service that publishes a host port in the baseline is one a developer
// reaches directly, so it is the one worth reaching in a workspace. The overlay
// cannot publish that host port itself - the baseline already holds it - so the
// proxy routes to the container port instead.
func RoutesFor(project *types.Project, services []string, host string) map[string]RouteOptions {
	routes := map[string]RouteOptions{}
	for _, name := range services {
		svc, ok := project.Services[name]
		if !ok {
			continue
		}
		port := 0
		for _, p := range svc.Ports {
			if p.Target > 0 {
				port = int(p.Target)
				break
			}
		}
		if port == 0 {
			for _, e := range svc.Expose {
				if n := atoi(e); n > 0 {
					port = n
					break
				}
			}
		}
		if port > 0 {
			routes[name] = RouteOptions{Host: host, Port: port}
		}
	}
	return routes
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// ContainerFor finds the baseline's container for one compose service.
//
// Under header routing the baseline's service keys are renamed, so the compose
// service label is looked up under both names rather than assumed.
func (r *Runner) ContainerFor(ctx context.Context, service string) (string, error) {
	for _, key := range []string{service, BaselineName(service)} {
		out, err := r.run(ctx, "docker", "ps", "--format", "{{.Names}}",
			"--filter", "label=com.docker.compose.project="+BaselineProject,
			"--filter", "label=com.docker.compose.service="+key)
		if err == nil && out != "" {
			return strings.Split(out, "\n")[0], nil
		}
	}
	return "", fmt.Errorf("no running baseline container for service %q", service)
}

// Exec runs a command inside a container with env set on the process. It is the
// seam the data package uses, so that package never shells out to Docker.
//
// Passing env through docker rather than a shell keeps SQL out of shell parsing
// entirely.
func (r *Runner) Exec(ctx context.Context, container string, env []string, args ...string) (string, error) {
	full := []string{"exec"}
	for _, e := range env {
		full = append(full, "-e", e)
	}
	full = append(full, container)
	return r.run(ctx, "docker", append(full, args...)...)
}

// WaitHealthy blocks until a container reports running, so a database is
// actually accepting connections before Bopper issues SQL against it.
func (r *Runner) WaitHealthy(ctx context.Context, container string, attempts int) error {
	var last error
	for i := 0; i < attempts; i++ {
		out, err := r.run(ctx, "docker", "inspect", "-f", "{{.State.Running}}", container)
		if err == nil && strings.TrimSpace(out) == "true" {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("container %s did not become ready: %v", container, last)
}
