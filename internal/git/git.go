// Package git wraps the git commands the workspace layer needs.
//
// The workspace layer is the only half of Bopper that knows about git. Keeping
// these calls behind one package is what lets the environment layer stay usable
// with jj, plain directories and CI checkouts.
package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// run executes git in dir and returns trimmed stdout.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out)), nil
}

// Root returns the top level of the working tree containing dir.
func Root(dir string) (string, error) {
	return run(dir, "rev-parse", "--show-toplevel")
}

// CommonDir returns the shared .git directory. In a linked worktree this is the
// parent repository's .git, not the worktree's own .git file, which makes it the
// right place to keep state that every workspace shares.
func CommonDir(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return out, nil
}

// CurrentBranch returns the checked out branch name.
func CurrentBranch(dir string) (string, error) {
	return run(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// BranchExists reports whether a local branch of this name exists.
func BranchExists(dir, branch string) bool {
	_, err := run(dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// AddWorktree creates a worktree at path. When the branch already exists it is
// checked out; otherwise it is created from base.
func AddWorktree(dir, path, branch, base string) error {
	args := []string{"worktree", "add", "-q"}
	if BranchExists(dir, branch) {
		args = append(args, path, branch)
	} else {
		args = append(args, "-b", branch, path, base)
	}
	_, err := run(dir, args...)
	return err
}

// RemoveWorktree removes a worktree. Only paths git already tracks as a worktree
// are accepted, which is why Bopper never needs to delete a directory itself.
func RemoveWorktree(dir, path string) error {
	_, err := run(dir, "worktree", "remove", "--force", path)
	return err
}

// DeleteBranch removes a local branch.
func DeleteBranch(dir, branch string) error {
	_, err := run(dir, "branch", "-D", branch)
	return err
}

// Prune drops worktree records whose directories are gone.
func Prune(dir string) error {
	_, err := run(dir, "worktree", "prune")
	return err
}

// CheckoutAll restores tracked files from the index. After the working directory
// of the main worktree is cloned over a fresh checkout, this rewrites only the
// files that differ between the two branches and leaves the rest untouched, so
// mtimes survive and incremental builds stay warm. Measured in spikes/b-reflink.md.
func CheckoutAll(dir string) error {
	_, err := run(dir, "checkout", "--", ".")
	return err
}

// Status returns porcelain status lines.
func Status(dir string) ([]string, error) {
	out, err := run(dir, "status", "--porcelain")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// ChangedFiles lists paths differing between two revisions.
func ChangedFiles(dir, from, to string) ([]string, error) {
	out, err := run(dir, "diff", "--name-only", from, to)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// MainWorktree returns the repository's main worktree, which is the baseline.
//
// Root reports the worktree containing dir, so inside a linked worktree it
// returns that workspace rather than the baseline. `git worktree list` always
// reports the main worktree first.
func MainWorktree(dir string) (string, error) {
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			return strings.TrimPrefix(line, "worktree "), nil
		}
	}
	return "", fmt.Errorf("could not determine the main worktree")
}
