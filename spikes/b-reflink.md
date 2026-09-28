# Spike B — reflink-cloned worktrees

**Phase 0. Run on macOS, APFS, Docker not involved. Git 2.x, 41,242 files, 1.2 GB.**

Scripts: [`gen-repo.py`](gen-repo.py), [`b-reflink.sh`](b-reflink.sh).

## Why a generated repository

The machine had 18 GB free with Docker already holding 11.8 GB of images, so
cloning a large monorepo and installing its dependencies would not fit safely.

Everything this spike measures is mechanical: whether a clone costs disk, whether
mtimes survive, whether git stays correct on top of a clone, whether an incremental
build stays warm. Those depend on file count, file sizes and directory shape, not on
whether the bytes are real npm packages. A generated tree measures them honestly and
lets the size be swept.

`gen-repo.py` builds a JS-monorepo shape: four service directories of tracked
TypeScript, plus 40,000 ignored dependency files in deep directories, 1.2 GB total,
with incompressible content so APFS cannot dedupe it and flatter the result.

**What this does not measure, and Phase 0 still owes:** how often a real branch
changes only one or two services. That needs real git history, not a fixture.

## Result — the clone is ~60x cheaper on disk and ~3x faster

Three consecutive runs:

| Operation                        | Wall time | Disk used |
| -------------------------------- | --------- | --------- |
| `cp -R` (a real copy)            | 23–35 s   | ~1305 MB  |
| `cp -c` (a reflink clone)        | 9 s       | 17–21 MB  |
| Full sequence (add, overlay, checkout) | 9–11 s | 22 MB   |

The clone is not literally free. The residual ~20 MB is filesystem metadata — new
directory entries and inodes for 41k files, roughly 0.5 KB each. Expect that cost to
track file *count*, not bytes.

## The sequence, which was an open question

```
1. git worktree add -b <branch> <dir>     # fresh checkout, new mtimes
2. overlay main's working directory       # cp -c, preserves mtimes, skips .git
3. git checkout -- .                      # rewrites only files the branch changes
```

Measured at each step, three runs, all stable:

- After step 2, mtimes match the main worktree exactly.
- After step 3, they still match. `git checkout` rewrites only the files that
  actually differ between the branches, and leaves the rest untouched.
- `git status` is clean apart from `?? build/` — the untracked build output that
  came across in the clone. That is the mechanism working.
- The incremental build stays warm: `make` reports up to date in the fresh worktree.
- The dependency tree arrives intact: 40,000 files present.

**Step 2 must skip `.git`.** In a linked worktree `.git` is a *file* pointing at the
parent repository. Overwriting it with the main repository's `.git` directory breaks
the worktree.

## git does not miss a cloned change

The dangerous failure would be a cloned file that git reports as clean while its
content differs — silent corruption of a worktree. Tested directly: same size, same
mtime, different content.

**git detected it.** The index stores inode and ctime alongside size and mtime, and a
cloned file always has a new inode, so git re-hashes rather than trusting the stat.
This is why the overlay in step 2 is safe.

Note that `core.checkStat = minimal` drops inode and ctime from that comparison.
Bopper should not set it, and should probably warn if a repository has.

## Two notes on method

**`cp -c` preserves mtimes in every form tested** — single file, `-R` on a directory,
per-entry loop, with and without `-p`. A 300 MB `cp -cp -R` used 0 MB, so `-p` does
not defeat the clone.

**An earlier version of this spike gave unstable results** — the incremental build
went cold in 5 of 5 runs, then stayed warm in others, with no change to the system
under test. The cause was the harness: it deleted each destination entry before
copying. Replacing that with `cp -R src/. dst/`, which merges into an existing
directory, made the result stable at 3 of 3. **A flaky measurement usually means a
flaky harness.** The delete was also unsafe — `rm -rf` on a loop variable — and the
script no longer contains one anywhere. Worktrees are removed with
`git worktree remove`, and scratch directories are left for a human to inspect.

## Still open

- Absolute paths inside cloned artifacts — venv shebangs, `compile_commands.json` —
  still point at the original checkout. Listed in `vision.md` as a hard edge,
  untested here.
- Linux behaviour. `cp --reflink=always` on btrfs and XFS is the equivalent, unrun.
- The residual metadata cost at much larger file counts, unswept.
