// Package clone copies directories with filesystem reflinks, so a workspace pays
// only for what it changes.
//
// Bopper shells out to cp rather than calling clonefile(2) or ioctl(FICLONE)
// directly. That is deliberate for now: spikes/b-reflink.md measured this exact
// command shape, and the platform cp already handles the fallbacks, permissions
// and directory merging that a hand-rolled walker would have to reimplement.
// Replacing it with a syscall walker is a later optimisation, not a correctness
// fix, and it should be measured against these numbers before it lands.
package clone

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Supported reports whether this platform has a cp that can reflink.
func Supported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

// args returns the cp invocation that clones rather than copies.
//
// macOS: -c forces clonefile(2) and fails rather than falling back, which is
// what we want; a silent full copy would quietly cost gigabytes.
// Linux: --reflink=always fails the same way on a filesystem without support.
func args() []string {
	if runtime.GOOS == "darwin" {
		return []string{"-c", "-R"}
	}
	return []string{"-a", "--reflink=always"}
}

// Dir clones src to dst, merging into dst if it already exists.
//
// The "src/." form is what makes the merge work. Plain "cp -R src dst" nests as
// dst/src when dst exists, which is why an earlier version of the spike deleted
// the destination first. That delete was both unnecessary and unsafe.
func Dir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	// NOT filepath.Join(src, "."): Join cleans the path and drops the trailing
	// "/.", turning the merge into a nesting copy that produces dst/src/. The
	// trailing element is the whole point, so build the string directly.
	sep := string(os.PathSeparator)
	return cp(src+sep+".", dst+sep)
}

// File clones a single file.
func File(src, dst string) error {
	if runtime.GOOS == "darwin" {
		return cp(src, dst, "-c")
	}
	return cp(src, dst, "--reflink=always", "-p")
}

func cp(src, dst string, extra ...string) error {
	a := extra
	if len(a) == 0 {
		a = args()
	}
	a = append(append([]string{}, a...), src, dst)
	cmd := exec.Command("cp", a...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp %s: %w: %s", strings.Join(a, " "), err,
			strings.TrimSpace(string(out)))
	}
	return nil
}

// Overlay clones every entry of src onto dst, skipping the named entries.
//
// Callers must skip ".git". In a linked worktree .git is a FILE pointing at the
// parent repository; overwriting it with the main repository's .git directory
// breaks the worktree outright.
func Overlay(src, dst string, skip ...string) error {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if skipped[name] {
			continue
		}
		s := filepath.Join(src, name)
		d := filepath.Join(dst, name)

		// Resolve the entry itself, not its target: a symlink to a directory
		// must be recreated as a symlink, not walked into.
		info, err := os.Lstat(s)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(s)
			if err != nil {
				return err
			}
			_ = os.Remove(d)
			if err := os.Symlink(target, d); err != nil {
				return err
			}
		case info.IsDir():
			if err := Dir(s, d); err != nil {
				return err
			}
		default:
			if err := File(s, d); err != nil {
				return err
			}
		}
	}
	return nil
}
