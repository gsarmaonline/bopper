package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsarmaonline/bopper/internal/clone"
)

func TestNormalizeID(t *testing.T) {
	cases := map[string]string{
		"feature-x":             "feature-x",
		"Feature/X":             "feature-x",
		"  spaced  name  ":      "spaced-name",
		"feat_123":              "feat-123",
		"---":                   "",
		strings.Repeat("a", 60): strings.Repeat("a", 40),
	}
	for in, want := range cases {
		if got := NormalizeID(in); got != want {
			t.Errorf("NormalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

// repo builds a git repository with a dependency directory that is ignored, so
// the test covers the thing reflink cloning exists for.
func repo(t *testing.T) string {
	t.Helper()
	if !clone.Supported() {
		t.Skip("no reflink cp on this platform")
	}
	dir := filepath.Join(t.TempDir(), "main")
	write(t, filepath.Join(dir, "app", "main.go"), "package main")
	write(t, filepath.Join(dir, ".gitignore"), "node_modules/\n")
	write(t, filepath.Join(dir, ".env"), "FOO=bar\n")
	for _, c := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"},
	} {
		cmd := exec.Command("git", c...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", c, err, out)
		}
	}
	// Untracked, ignored build output: it must come along with the clone.
	write(t, filepath.Join(dir, "node_modules", "dep", "index.js"), "dep")
	return dir
}

func TestUpCreatesAWorkingWorkspace(t *testing.T) {
	root := repo(t)
	m, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.Up("Feature/X", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Down(w.ID, DownOptions{DeleteBranch: true}) })

	if w.ID != "feature-x" {
		t.Fatalf("ID = %q", w.ID)
	}
	if got := read(t, filepath.Join(w.Dir, "app", "main.go")); got != "package main" {
		t.Fatalf("tracked file missing: %q", got)
	}
	// The point of the reflink clone: ignored artifacts arrive without a rebuild.
	if got := read(t, filepath.Join(w.Dir, "node_modules", "dep", "index.js")); got != "dep" {
		t.Fatalf("ignored artifacts did not come along: %q", got)
	}
	// .git must remain the worktree's own file, not the parent's directory.
	info, err := os.Stat(filepath.Join(w.Dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		t.Fatal(".git is a directory: the parent repository's .git was cloned over the worktree")
	}
	env := read(t, filepath.Join(w.Dir, ".env"))
	if !strings.Contains(env, "BOPPER_WORKSPACE=feature-x") || !strings.Contains(env, "FOO=bar") {
		t.Fatalf(".env not patched correctly:\n%s", env)
	}
	// The baseline must be untouched.
	if strings.Contains(read(t, filepath.Join(root, ".env")), "BOPPER_WORKSPACE") {
		t.Fatal("bop up modified the baseline's .env")
	}
}

func TestUpRejectsDuplicates(t *testing.T) {
	root := repo(t)
	m, _ := Open(root)
	w, err := m.Up("dup", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Down(w.ID, DownOptions{DeleteBranch: true}) })
	if _, err := m.Up("dup", UpOptions{}); err == nil {
		t.Fatal("second up succeeded; want an error")
	}
}

func TestDownRemovesEverything(t *testing.T) {
	root := repo(t)
	m, _ := Open(root)
	w, err := m.Up("gone", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Down(w.ID, DownOptions{DeleteBranch: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Dir); !os.IsNotExist(err) {
		t.Fatalf("directory survived down: %v", err)
	}
	ws, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 0 {
		t.Fatalf("List still reports %v", ws)
	}
}

// A directory deleted by hand must not wedge the tool.
func TestUpRecoversFromAHandDeletedWorktree(t *testing.T) {
	root := repo(t)
	m, _ := Open(root)
	w, err := m.Up("stale", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(w.Dir); err != nil {
		t.Fatal(err)
	}
	w2, err := m.Up("stale", UpOptions{})
	if err != nil {
		t.Fatalf("up after a hand-deleted worktree failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Down(w2.ID, DownOptions{DeleteBranch: true}) })
}

func TestListDropsVanishedWorkspaces(t *testing.T) {
	root := repo(t)
	m, _ := Open(root)
	w, err := m.Up("ghost", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(w.Dir); err != nil {
		t.Fatal(err)
	}
	ws, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 0 {
		t.Fatalf("List reported a workspace whose directory is gone: %v", ws)
	}
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
