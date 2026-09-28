package clone

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// skipUnlessReflink skips when the filesystem under test cannot reflink, which
// is normal on CI overlayfs and on tmpfs.
func skipUnlessReflink(t *testing.T) {
	t.Helper()
	if !Supported() {
		t.Skip("no reflink cp on this platform")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "probe")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := File(src, filepath.Join(dir, "probe2")); err != nil {
		t.Skipf("filesystem does not support reflink: %v", err)
	}
}

func TestDirMergesRatherThanNests(t *testing.T) {
	skipUnlessReflink(t)
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	mustWrite(t, filepath.Join(src, "a", "b.txt"), "hello")
	mustWrite(t, filepath.Join(dst, "keep.txt"), "keep")

	if err := Dir(src, dst); err != nil {
		t.Fatal(err)
	}

	// The whole point: contents land directly in dst.
	if got := read(t, filepath.Join(dst, "a", "b.txt")); got != "hello" {
		t.Fatalf("dst/a/b.txt = %q, want %q", got, "hello")
	}
	// A regression guard for filepath.Join(src, ".") cleaning away the trailing
	// "/.", which silently turns the merge into a nesting copy.
	if _, err := os.Stat(filepath.Join(dst, "src")); err == nil {
		t.Fatal("dst/src exists: the copy nested instead of merging")
	}
	if got := read(t, filepath.Join(dst, "keep.txt")); got != "keep" {
		t.Fatal("merging destroyed a file already in dst")
	}
}

func TestClonePreservesMtime(t *testing.T) {
	skipUnlessReflink(t)
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustWrite(t, filepath.Join(src, "f.txt"), "data")

	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(src, "f.txt"), old, old); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "dst")
	if err := Dir(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dst, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// Mtimes must survive, or every incremental build in a new workspace runs cold.
	if !info.ModTime().Truncate(time.Second).Equal(old) {
		t.Fatalf("mtime = %v, want %v", info.ModTime(), old)
	}
}

func TestOverlaySkipsNamedEntries(t *testing.T) {
	skipUnlessReflink(t)
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	mustWrite(t, filepath.Join(src, ".git", "config"), "repo config")
	mustWrite(t, filepath.Join(src, "app.txt"), "app")
	mustWrite(t, filepath.Join(dst, ".git"), "gitdir: /elsewhere")

	if err := Overlay(src, dst, ".git"); err != nil {
		t.Fatal(err)
	}
	// In a linked worktree .git is a FILE. Overwriting it with the parent's .git
	// directory breaks the worktree outright.
	if got := read(t, filepath.Join(dst, ".git")); got != "gitdir: /elsewhere" {
		t.Fatalf(".git was overwritten: %q", got)
	}
	if got := read(t, filepath.Join(dst, "app.txt")); got != "app" {
		t.Fatalf("app.txt = %q", got)
	}
}

func TestOverlayRecreatesSymlinks(t *testing.T) {
	skipUnlessReflink(t)
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustWrite(t, filepath.Join(src, "real", "f.txt"), "x")
	if err := os.Symlink("real", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "dst")
	if err := Overlay(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dst, "link"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was walked into instead of recreated")
	}
}

func mustWrite(t *testing.T, path, content string) {
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
