// Package workspace is the workspace layer: git and the filesystem, nothing else.
//
// It never calls Docker. Its entire output is a workspace ID and a directory,
// which is the narrow boundary the environment layer builds on. That boundary is
// what keeps the environment layer usable with jj, plain directories and CI
// checkouts, and it lets each half be tested alone.
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gsarmaonline/bopper/internal/clone"
	"github.com/gsarmaonline/bopper/internal/envfile"
	"github.com/gsarmaonline/bopper/internal/git"
)

// Workspace is one worktree plus the metadata Bopper keeps about it.
type Workspace struct {
	ID      string    `json:"id"`
	Branch  string    `json:"branch"`
	Dir     string    `json:"dir"`
	Base    string    `json:"base"`
	Created time.Time `json:"created"`
}

// Host is where this workspace will be reachable once the environment layer runs.
func (w Workspace) Host() string { return w.ID + ".localhost" }

// Manager owns the workspace list for one repository.
type Manager struct {
	Root  string // the MAIN worktree, which is the baseline
	Here  string // the worktree the command was run from
	state string // where the list is stored
}

// Open locates the repository containing dir and prepares its manager.
//
// Root is deliberately the main worktree, not the one containing dir. Inside a
// workspace the two differ, and every comparison Bopper makes is against the
// baseline.
func Open(dir string) (*Manager, error) {
	here, err := git.Root(dir)
	if err != nil {
		return nil, fmt.Errorf("not inside a git repository: %w", err)
	}
	root, err := git.MainWorktree(dir)
	if err != nil {
		return nil, err
	}
	common, err := git.CommonDir(dir)
	if err != nil {
		return nil, err
	}
	// State lives in the shared .git directory, never in the working tree, so it
	// cannot show up as an untracked file or be cloned into a workspace.
	return &Manager{
		Root:  root,
		Here:  here,
		state: filepath.Join(common, "bopper", "workspaces.json"),
	}, nil
}

var idRe = regexp.MustCompile(`[^a-z0-9-]+`)

// NormalizeID turns a branch-ish name into something usable as a hostname label,
// a container name and a directory name all at once.
func NormalizeID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = idRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

func (m *Manager) load() ([]Workspace, error) {
	b, err := os.ReadFile(m.state)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ws []Workspace
	if err := json.Unmarshal(b, &ws); err != nil {
		return nil, fmt.Errorf("reading %s: %w", m.state, err)
	}
	return ws, nil
}

func (m *Manager) save(ws []Workspace) error {
	if err := os.MkdirAll(filepath.Dir(m.state), 0o755); err != nil {
		return err
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].ID < ws[j].ID })
	b, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.state, append(b, '\n'), 0o644)
}

// List returns the known workspaces, dropping any whose directory has gone.
func (m *Manager) List() ([]Workspace, error) {
	ws, err := m.load()
	if err != nil {
		return nil, err
	}
	live := ws[:0]
	for _, w := range ws {
		if _, err := os.Stat(w.Dir); err == nil {
			live = append(live, w)
		}
	}
	return live, nil
}

// Get returns one workspace by ID.
func (m *Manager) Get(id string) (Workspace, bool, error) {
	ws, err := m.List()
	if err != nil {
		return Workspace{}, false, err
	}
	for _, w := range ws {
		if w.ID == id {
			return w, true, nil
		}
	}
	return Workspace{}, false, nil
}

// UpOptions configures Up.
type UpOptions struct {
	Base   string // branch to fork from; defaults to the current branch
	DirFor func(root, id string) string
}

// Up creates a workspace: a worktree, a reflinked copy of the main working
// directory, and a patched .env.
//
// The sequence is the one measured in spikes/b-reflink.md:
//
//  1. git worktree add      - a fresh checkout, with new mtimes
//  2. overlay with cp -c    - restores the main worktree's content and mtimes,
//     and brings ignored build artifacts along free
//  3. git checkout -- .     - rewrites only the files the branch actually changes
//
// Step 2 must skip .git. Step 3 is safe because git's index compares inode and
// ctime as well as size and mtime, so a cloned file is always re-hashed rather
// than trusted; a same-size, same-mtime change is still detected.
func (m *Manager) Up(name string, opts UpOptions) (Workspace, error) {
	if !clone.Supported() {
		return Workspace{}, fmt.Errorf("reflink clone is not supported on this platform")
	}
	id := NormalizeID(name)
	if id == "" {
		return Workspace{}, fmt.Errorf("workspace name %q normalizes to empty", name)
	}
	if existing, found, err := m.Get(id); err != nil {
		return Workspace{}, err
	} else if found {
		return existing, fmt.Errorf("workspace %q already exists at %s", id, existing.Dir)
	}

	base := opts.Base
	if base == "" {
		b, err := git.CurrentBranch(m.Root)
		if err != nil {
			return Workspace{}, err
		}
		base = b
	}

	dirFor := opts.DirFor
	if dirFor == nil {
		dirFor = defaultDir
	}
	dir := dirFor(m.Root, id)
	if _, err := os.Stat(dir); err == nil {
		return Workspace{}, fmt.Errorf("%s already exists", dir)
	}

	// Drop registrations whose directory a human already deleted by hand.
	// Without this, git refuses the add with "missing but already registered".
	_ = git.Prune(m.Root)

	if err := git.AddWorktree(m.Root, dir, id, base); err != nil {
		return Workspace{}, err
	}

	// From here on a failure leaves a worktree behind, so unwind it.
	fail := func(err error) (Workspace, error) {
		_ = git.RemoveWorktree(m.Root, dir)
		_ = git.Prune(m.Root)
		return Workspace{}, err
	}

	if err := clone.Overlay(m.Root, dir, ".git"); err != nil {
		return fail(fmt.Errorf("cloning the working directory: %w", err))
	}
	if err := git.CheckoutAll(dir); err != nil {
		return fail(fmt.Errorf("restoring tracked files: %w", err))
	}

	w := Workspace{ID: id, Branch: id, Dir: dir, Base: base, Created: time.Now().UTC()}
	if err := patchEnv(dir, w); err != nil {
		return fail(fmt.Errorf("patching .env: %w", err))
	}

	ws, err := m.load()
	if err != nil {
		return fail(err)
	}
	if err := m.save(append(ws, w)); err != nil {
		return fail(err)
	}
	return w, nil
}

// DownOptions configures Down.
type DownOptions struct {
	DeleteBranch bool
}

// Down removes a workspace. The directory is only ever removed through
// git worktree remove, which refuses any path git does not already track as a
// worktree. Bopper never deletes a directory by path itself.
func (m *Manager) Down(id string, opts DownOptions) error {
	w, found, err := m.Get(id)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no workspace %q", id)
	}
	if err := git.RemoveWorktree(m.Root, w.Dir); err != nil {
		return err
	}
	_ = git.Prune(m.Root)
	if opts.DeleteBranch {
		if err := git.DeleteBranch(m.Root, w.Branch); err != nil {
			return fmt.Errorf("worktree removed, but branch %s remains: %w", w.Branch, err)
		}
	}
	all, err := m.load()
	if err != nil {
		return err
	}
	kept := all[:0]
	for _, x := range all {
		if x.ID != id {
			kept = append(kept, x)
		}
	}
	return m.save(kept)
}

func defaultDir(root, id string) string {
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+id)
}

// patchEnv writes the managed block into .env. A developer's own lines above the
// marker survive, and repeated runs are idempotent.
//
// The database entry is deliberately NOT rewritten. A workspace shares the
// baseline database by default; only a branch that changes a migration or asks
// for writes gets its own, and that is the environment layer's decision to make.
func patchEnv(dir string, w Workspace) error {
	return envfile.Patch(filepath.Join(dir, ".env"), [][2]string{
		{"BOPPER_WORKSPACE", w.ID},
		{"BOPPER_HOST", w.Host()},
		{"COMPOSE_PROJECT_NAME", "bopper-ws-" + w.ID},
	})
}
