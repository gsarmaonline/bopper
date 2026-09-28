package environment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ProxyName is the shared Traefik container. One proxy serves every workspace,
// which is what lets each be reached at its own hostname without any workspace
// publishing a host port of its own.
const ProxyName = "bopper-traefik"

// ProxyImage is pinned. An unpinned tag would move under the tool and change
// routing behaviour without any change to Bopper.
const ProxyImage = "traefik:v3.3"

// Bopper configures Traefik with the FILE provider, not the Docker provider.
//
// The Docker provider would have Traefik re-discover, through the Docker socket,
// what Bopper already knows for certain: it just started those containers. That
// indirection buys nothing and costs a hard dependency on mounting the socket
// into a container - which fails outright wherever socket sharing is disabled,
// as it is under Docker Desktop's default-socket setting and under Enhanced
// Container Isolation. Mounting the socket also hands the proxy full control of
// the daemon, which is a poor trade for a local development tool.
//
// Writing a dynamic configuration file instead is deterministic, needs no
// socket, and works in restricted setups.
const proxyDynamicDir = "traefik"

// ProxyPort is the host port the proxy listens on. Port 80 is the obvious
// choice for "<id>.localhost", but developer stacks very often publish 80
// already, and a first run that fails on a port clash is a bad introduction.
// Override with BOPPER_PROXY_PORT.
func ProxyPort() int {
	if v := os.Getenv("BOPPER_PROXY_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 8080
}

// URL is where a workspace is reachable through the proxy.
func URL(host string) string {
	if p := ProxyPort(); p != 80 {
		return fmt.Sprintf("http://%s:%d", host, p)
	}
	return "http://" + host
}

func (r *Runner) dynamicDir() string { return filepath.Join(r.StateDir, proxyDynamicDir) }

// WriteRoutes publishes one workspace's routing rules. It reports whether the
// file actually changed, so the caller can avoid a needless proxy reload.
//
// Traefik watches the directory, but that watch is not dependable: on Docker
// Desktop the mount crosses a VM boundary and inotify events for newly created
// files are frequently lost. Observed directly - a route file written seconds
// after the proxy started was never picked up, and every request 404'd, while
// the identical file was loaded correctly when present at startup. So the watch
// is kept as the fast path and ReloadProxy is the guarantee.
func (r *Runner) WriteRoutes(id string, routes map[string]RouteOptions) (bool, error) {
	dir := r.dynamicDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	path := filepath.Join(dir, id+".yaml")
	if len(routes) == 0 {
		if _, err := os.Stat(path); err == nil {
			return true, os.Remove(path)
		}
		return false, nil
	}

	routers := map[string]any{}
	services := map[string]any{}
	for svc, r0 := range routes {
		key := id + "-" + svc
		routers[key] = map[string]any{
			"rule":        fmt.Sprintf("Host(`%s`)", r0.Host),
			"service":     key,
			"entryPoints": []string{"web"},
		}
		// Address the container by its overlay name. That name is unique across
		// every network the proxy is attached to, whereas the plain service name
		// also exists on the baseline network.
		services[key] = map[string]any{
			"loadBalancer": map[string]any{
				"servers": []any{
					map[string]any{"url": fmt.Sprintf("http://%s:%d", OverlayName(svc, id), r0.Port)},
				},
			},
		}
	}
	doc, err := yaml.Marshal(map[string]any{
		"http": map[string]any{"routers": routers, "services": services},
	})
	if err != nil {
		return false, err
	}
	if old, err := os.ReadFile(path); err == nil && string(old) == string(doc) {
		return false, nil
	}
	return true, os.WriteFile(path, doc, 0o644)
}

// RemoveRoutes drops a workspace's routing rules, reporting whether anything
// was there to remove.
func (r *Runner) RemoveRoutes(id string) (bool, error) {
	err := os.Remove(filepath.Join(r.dynamicDir(), id+".yaml"))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// ReloadProxy restarts the proxy so a route change is certain to take effect.
// It costs about a second and briefly interrupts every workspace, which is why
// callers only invoke it when a route file actually changed.
func (r *Runner) ReloadProxy(ctx context.Context) error {
	out, err := r.run(ctx, "docker", "ps", "-q", "--filter", "name=^/"+ProxyName+"$")
	if err != nil || out == "" {
		return nil
	}
	_, err = r.run(ctx, "docker", "restart", "-t", "2", ProxyName)
	return err
}

// EnsureProxy starts the shared proxy if it is not already running.
func (r *Runner) EnsureProxy(ctx context.Context) error {
	out, err := r.run(ctx, "docker", "ps", "-q", "--filter", "name=^/"+ProxyName+"$")
	if err == nil && out != "" {
		return nil
	}
	_, _ = r.run(ctx, "docker", "rm", "-f", ProxyName)

	if err := r.EnsureNetwork(ctx, BaselineNetwork); err != nil {
		return err
	}
	dir := r.dynamicDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	_, err = r.run(ctx, "docker", "run", "-d",
		"--name", ProxyName,
		"--restart", "unless-stopped",
		"--network", BaselineNetwork,
		"-p", fmt.Sprintf("%d:80", ProxyPort()),
		"-v", dir+":/etc/traefik/dynamic:ro",
		"--label", "bopper.managed=true",
		ProxyImage,
		"--providers.file.directory=/etc/traefik/dynamic",
		"--providers.file.watch=true",
		"--entrypoints.web.address=:80",
		"--accesslog=true",
		"--log.level=INFO",
	)
	if err != nil {
		return fmt.Errorf("starting the proxy on port %d (set BOPPER_PROXY_PORT to change it): %w",
			ProxyPort(), err)
	}
	return nil
}

// EnsureNetwork creates a network if it does not exist.
func (r *Runner) EnsureNetwork(ctx context.Context, name string) error {
	out, err := r.run(ctx, "docker", "network", "ls", "-q", "--filter", "name=^"+name+"$")
	if err == nil && out != "" {
		return nil
	}
	_, err = r.run(ctx, "docker", "network", "create", name)
	return err
}

// AttachProxy connects the proxy to a workspace network, so it can reach the
// overlay's containers by name.
func (r *Runner) AttachProxy(ctx context.Context, network string) error {
	out, err := r.run(ctx, "docker", "inspect", "-f",
		"{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}", ProxyName)
	if err != nil {
		return err
	}
	for _, n := range strings.Fields(out) {
		if n == network {
			return nil
		}
	}
	_, err = r.run(ctx, "docker", "network", "connect", network, ProxyName)
	return err
}

// DetachProxy disconnects the proxy from a workspace network, so the network
// can be removed when the workspace goes away.
func (r *Runner) DetachProxy(ctx context.Context, network string) {
	_, _ = r.run(ctx, "docker", "network", "disconnect", "-f", network, ProxyName)
}

// StopProxy removes the shared proxy.
func (r *Runner) StopProxy(ctx context.Context) error {
	_, err := r.run(ctx, "docker", "rm", "-f", ProxyName)
	return err
}
