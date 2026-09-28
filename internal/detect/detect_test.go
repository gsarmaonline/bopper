package detect

import (
	"context"

	"github.com/gsarmaonline/bopper/internal/envfile"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two Compose patterns occur in the wild, and they behave very differently.
//
// isolated: each service builds from its own directory. A change to one service
// flips only that service. This is what most multi-service repos do.
//
// monorepo: every service builds from the repository root, as immich does with
// "context: ../". Any change anywhere flips every service. That is correct, not
// a defect, and it is why Spike C's honest figures are worse than its flattering
// ones: shared code and lockfiles rebuild everything.
func fixtureIsolated(t *testing.T, dir, workspace string) {
	t.Helper()
	write(t, filepath.Join(dir, "compose.yaml"), `
name: demo
services:
  orders:
    build: ./orders
    environment:
      - WORKSPACE=${BOPPER_WORKSPACE:-baseline}
  payments:
    build: ./payments
  cache:
    image: redis:7-alpine
`)
	for _, svc := range []string{"orders", "payments"} {
		write(t, filepath.Join(dir, svc, "Dockerfile"), "FROM alpine:3.20\nCOPY . /app\n")
		write(t, filepath.Join(dir, svc, "app.js"), svc+" v1")
		write(t, filepath.Join(dir, svc, ".dockerignore"), "*.log\nnode_modules/\n")
	}
	writeEnv(t, dir, workspace)
}

func fixtureMonorepo(t *testing.T, dir, workspace string) {
	t.Helper()
	write(t, filepath.Join(dir, "compose.yaml"), `
name: demo
services:
  orders:
    build:
      context: .
      dockerfile: orders/Dockerfile
  payments:
    build:
      context: .
      dockerfile: payments/Dockerfile
`)
	// compose.yaml is ignored, as a real project would: it configures the build,
	// it is not an input to it. .env is deliberately NOT ignored, so these tests
	// exercise Bopper stripping its own managed block out of it.
	write(t, filepath.Join(dir, ".dockerignore"), "**/*.log\nnode_modules/\ncompose.yaml\n")
	for _, svc := range []string{"orders", "payments"} {
		write(t, filepath.Join(dir, svc, "Dockerfile"), "FROM alpine:3.20\nCOPY "+svc+" /app\n")
		write(t, filepath.Join(dir, svc, "app.js"), svc+" v1")
	}
	write(t, filepath.Join(dir, "lock.json"), `{"deps":1}`)
	writeEnv(t, dir, workspace)
}

func writeEnv(t *testing.T, dir, workspace string) {
	t.Helper()
	write(t, filepath.Join(dir, ".env"), "FOO=bar\n")
	if workspace == "" {
		return
	}
	// Write the managed block through the real code path, so the tests exercise
	// what bop up actually produces.
	if err := envfile.Patch(filepath.Join(dir, ".env"), [][2]string{
		{"BOPPER_WORKSPACE", workspace},
		{"BOPPER_HOST", workspace + ".localhost"},
		{"COMPOSE_PROJECT_NAME", "bopper-ws-" + workspace},
	}); err != nil {
		t.Fatal(err)
	}
}

// pair builds a baseline and a workspace copy, then applies mutate to the
// workspace and returns the resulting changes.
func pair(t *testing.T, build func(*testing.T, string, string), mutate func(ws string)) []Change {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "base")
	ws := filepath.Join(root, "ws")
	build(t, base, "")
	build(t, ws, "feature-x")
	if mutate != nil {
		mutate(ws)
	}

	ctx := context.Background()
	bp, err := Load(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	wp, err := Load(ctx, ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	bf, err := bp.Fingerprints()
	if err != nil {
		t.Fatal(err)
	}
	wf, err := wp.Fingerprints()
	if err != nil {
		t.Fatal(err)
	}
	return Diff(bf, wf)
}

// The most important test in the package. A workspace that changes nothing must
// report nothing, even though it lives at a different path and its .env names it.
func TestIdenticalCheckoutsReportNoChange(t *testing.T) {
	for name, build := range map[string]func(*testing.T, string, string){
		"isolated": fixtureIsolated,
		"monorepo": fixtureMonorepo,
	} {
		t.Run(name, func(t *testing.T) {
			if got := pair(t, build, nil); len(got) != 0 {
				t.Fatalf("unchanged workspace reported %v", names(got))
			}
		})
	}
}

func TestSourceChangeFlipsOneService(t *testing.T) {
	got := pair(t, fixtureIsolated, func(ws string) {
		write(t, filepath.Join(ws, "orders", "app.js"), "orders v2")
	})
	assertChanged(t, got, map[string]string{"orders": "build"})
}

func TestComposeConfigChangeIsDetectedAsConfig(t *testing.T) {
	got := pair(t, fixtureIsolated, func(ws string) {
		s := read(t, filepath.Join(ws, "compose.yaml"))
		s = strings.Replace(s, "  cache:\n    image: redis:7-alpine",
			"  cache:\n    image: redis:7-alpine\n    command: [\"redis-server\",\"--port\",\"7000\"]", 1)
		write(t, filepath.Join(ws, "compose.yaml"), s)
	})
	assertChanged(t, got, map[string]string{"cache": "config"})
}

func TestDockerignoredFileIsNotAnInput(t *testing.T) {
	got := pair(t, fixtureIsolated, func(ws string) {
		write(t, filepath.Join(ws, "orders", "debug.log"), "noise")
		write(t, filepath.Join(ws, "orders", "node_modules", "dep", "index.js"), "dep")
	})
	if len(got) != 0 {
		t.Fatalf("ignored files counted as inputs: %v", names(got))
	}
}

// A shared file in both build contexts must flip both services. This is the
// mechanism that makes input hashing correct, and the reason Spike C's honest
// numbers are worse than its flattering ones.
func TestSharedFileFlipsEveryDependentService(t *testing.T) {
	got := pair(t, fixtureMonorepo, func(ws string) {
		write(t, filepath.Join(ws, "lock.json"), `{"deps":2}`)
	})
	assertChanged(t, got, map[string]string{"orders": "build", "payments": "build"})
}

func TestDockerfileChangeFlipsItsService(t *testing.T) {
	got := pair(t, fixtureIsolated, func(ws string) {
		write(t, filepath.Join(ws, "payments", "Dockerfile"),
			"FROM alpine:3.21\nCOPY . /app\n")
	})
	assertChanged(t, got, map[string]string{"payments": "build"})
}

func TestAddedAndRemovedServices(t *testing.T) {
	got := pair(t, fixtureIsolated, func(ws string) {
		s := read(t, filepath.Join(ws, "compose.yaml"))
		s = strings.Replace(s, "  cache:\n    image: redis:7-alpine\n",
			"  queue:\n    image: rabbitmq:3\n", 1)
		write(t, filepath.Join(ws, "compose.yaml"), s)
	})
	assertChanged(t, got, map[string]string{"queue": "added", "cache": "removed"})
}

func assertChanged(t *testing.T, got []Change, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("changed = %v, want %v", names(got), want)
	}
	for _, c := range got {
		reason, ok := want[c.Service]
		if !ok {
			t.Fatalf("unexpected change %s (%s); want %v", c.Service, c.Reason, want)
		}
		if c.Reason != reason {
			t.Errorf("%s: reason = %q, want %q", c.Service, c.Reason, reason)
		}
	}
}

func names(cs []Change) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Service+":"+c.Reason)
	}
	return out
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
