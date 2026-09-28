#!/usr/bin/env bash
# Spike B: what does a reflink-cloned worktree cost, and does git stay correct
# on top of one?
#
# NOTE ON SAFETY: this script never runs `rm -rf` on a variable path. Worktrees
# are removed with `git worktree remove`, which only ever touches a path git
# already tracks as a worktree. Scratch directories are created fresh with a
# unique suffix and left in place; the script prints one cleanup line at the end
# for you to inspect and run yourself.
#
# Files are overlaid with `cp -R src/. dst/`, which MERGES into an existing
# directory. That is why no delete is needed before the copy.
#
# Run: ./spikes/b-reflink.sh [work-dir]

set -eu
WORK="${1:-${TMPDIR:-/tmp}/bspike}"
MAIN="$WORK/main"
VOL=/System/Volumes/Data
RUN=$(date +%s)

[ -d "$MAIN/.git" ] || { echo "no generated repo at $MAIN; run gen-repo.py first"; exit 1; }

free_kb() { sync; sleep 1; df -k "$VOL" | tail -1 | awk '{print $4}'; }
human()   { awk -v k="$1" 'BEGIN{printf "%.0f MB", k/1024}'; }

# Overlay MAIN's working directory onto $1, preserving mtimes, skipping .git.
# In a linked worktree .git is a FILE pointing at the parent repo; clobbering it
# with MAIN's .git directory would break the worktree.
overlay() {
  local dst="$1" e
  for e in "$MAIN"/* "$MAIN"/.[!.]*; do
    [ -e "$e" ] || continue
    local base; base=$(basename "$e")
    [ "$base" = ".git" ] && continue
    if [ -d "$e" ]; then
      mkdir -p "$dst/$base"
      cp -c -R "$e/." "$dst/$base/"
    else
      cp -c "$e" "$dst/$base"
    fi
  done
}

echo "=== Fixture ==="
echo "  files: $(find "$MAIN" -type f | wc -l | tr -d ' ')   size: $(du -sh "$MAIN" | cut -f1)"
MSRC=$(stat -f %m "$MAIN/orders/src/mod_0000.ts")

# ------------------------------------------------------------ copy vs clone
echo
echo "=== cp -R (a real copy) vs cp -c (a reflink clone) ==="
B=$(free_kb); S=$(date +%s)
cp -R "$MAIN" "$WORK/copy_$RUN"
E=$(date +%s); A=$(free_kb)
echo "  cp -R:  $((E-S))s   $(human $((B-A)))"

B=$(free_kb); S=$(date +%s)
cp -c -R "$MAIN" "$WORK/clone_$RUN"
E=$(date +%s); A=$(free_kb)
echo "  cp -c:  $((E-S))s   $(human $((B-A)))"
CM=$(stat -f %m "$WORK/clone_$RUN/orders/src/mod_0000.ts")
echo "  mtime:  $([ "$CM" = "$MSRC" ] && echo PRESERVED || echo CHANGED)"

# --------------------------------------------------- the full Bopper sequence
WS="$WORK/ws_$RUN"
BR="spike-$RUN"
echo
echo "=== Bopper sequence: worktree add -> overlay -> git checkout ==="
B=$(free_kb); S=$(date +%s)
git -C "$MAIN" worktree add -q "$WS" -b "$BR" main
T1=$(date +%s); echo "  1. worktree add:  $((T1-S))s   (fresh checkout, new mtimes)"
overlay "$WS"
T2=$(date +%s); A2=$(stat -f %m "$WS/orders/src/mod_0000.ts")
echo "  2. overlay:       $((T2-T1))s   mtime $([ "$A2" = "$MSRC" ] && echo preserved || echo REWRITTEN)"
echo "     git sees $(git -C "$WS" status --porcelain | wc -l | tr -d ' ') changed file(s)"
git -C "$WS" checkout -q -- .
T3=$(date +%s); A3=$(stat -f %m "$WS/orders/src/mod_0000.ts"); A=$(free_kb)
echo "  3. git checkout:  $((T3-T2))s   mtime $([ "$A3" = "$MSRC" ] && echo preserved || echo REWRITTEN)"
echo "  total: $((T3-S))s   disk: $(human $((B-A)))"

echo
echo "=== Correctness ==="
ST=$(git -C "$WS" status --porcelain || true)
echo "  git status:      $([ -z "$ST" ] && echo clean || echo "DIRTY ($(echo "$ST" | wc -l | tr -d ' ') files)")"
echo "  deps present:    $(find "$WS/node_modules" -type f 2>/dev/null | wc -l | tr -d ' ') files"
if [ -f "$MAIN/Makefile" ]; then
  (cd "$MAIN" && make -s >/dev/null 2>&1) || true
  OUT=$( (cd "$WS" && make -s 2>&1 | tail -1) || true )
  echo "  incremental build: ${OUT:-UP-TO-DATE (stayed warm)}"
fi

echo
echo "Cleanup (inspect, then run yourself):"
echo "  git -C $MAIN worktree remove --force $WS && git -C $MAIN branch -D $BR"
echo "  rm -rf $WORK/copy_$RUN $WORK/clone_$RUN"
