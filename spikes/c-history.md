# Spike C — how many services does one branch change?

**Phase 0.** Script: [`c-history.py`](c-history.py).

This is the number the whole design rests on. `vision.md` asserts that "most branches
change one or two services" and measured nothing. A fixture cannot answer it; it needs
real history.

## Method

Two real multi-service repositories, cloned history-only with
`--filter=blob:none --no-checkout` — 26 MB and 31 MB, because `--name-only` needs
trees, not file contents.

Walk **first-parent** history. On a squash-merge repo each first-parent commit is one
merged PR; on a merge-commit repo each merge is one PR and git shows its first-parent
diff. Either way one first-parent commit approximates one branch's change set, which
is the unit Bopper cares about — not individual commits, which are finer-grained than
a branch.

Map each changed path to a service by directory prefix. The mapping is declared per
repo rather than parsed from the Compose file, because neither file carries a usable
per-service build context: immich builds every service from `context: ../`, and
penpot's published Compose ships prebuilt images. A Bopper user would configure it the
same way.

Branches touching no service at all — docs and CI only — are excluded from the
percentages. They would never start a stack, so counting them would flatter the
result. They are 24% of penpot's and 41% of immich's first-parent commits, which is
its own favourable point: a large share of branches need no environment whatsoever.

## Result, and the sensitivity that matters

Two mappings were run. The **narrow** one counts only each service's own directory.
The **shared** one also maps shared code and lockfiles to every service that depends
on them, because input hashing rebuilds a service whenever anything in its build
context changes — including `common/`, `packages/sdk/` and `pnpm-lock.yaml`.

| Repo   | Branches | Mapping | 1 service | 1 or 2 | 3+    |
| ------ | -------- | ------- | --------- | ------ | ----- |
| immich | 6,393    | narrow  | 82.8%     | 97.0%  | 3.0%  |
| immich | 6,505    | shared  | 75.4%     | 83.5%  | 16.5% |
| penpot | 6,568    | narrow  | 88.4%     | 98.6%  | 1.4%  |
| penpot | 6,940    | shared  | 73.4%     | 78.6%  | 21.4% |

**Take the shared numbers as the real ones.** The narrow mapping describes a tool that
misses indirect changes, which is precisely the failure mode input hashing exists to
prevent. The design's own correctness is what creates the pessimism, so it would be
dishonest to quote the flattering column.

## What this means for the design

**The premise holds.** Roughly three quarters of service-touching branches change
exactly one service, and around 80% change at most two, across two unrelated stacks
and two ecosystems. An overlay model built on that will pay off for most branches.

**But the tail is not small, and it is structural.** 16–21% of branches touch three or
more services, and they cluster on one cause: shared code and lockfiles. In immich,
lockfile changes alone rebuild all four services and are 6.4% of branches. For those,
an overlay gives no saving at all — the worktree rebuilds and runs the whole stack.

Two consequences worth designing around:

1. **`bop up` must degrade gracefully to a full stack.** It is not an edge case at
   one in five branches. The user should be told plainly that this branch touches
   everything, and roughly what it will cost, rather than discovering it.
2. **Lockfile-only changes may deserve a narrower rule.** A lockfile edit that adds a
   dependency used by one service still, by strict input hashing, rebuilds every
   service whose context contains the lockfile. A per-service dependency closure
   would cut the worst bucket, at the cost of ecosystem-specific logic. Worth
   revisiting in Phase 2, not before.

## Limits of this measurement

- Two repositories, both open-source, both web stacks. A repo with a different shape —
  many small services, or one monolith plus satellites — could differ.
- The service mapping is a judgement call, and the gap between the two mappings shows
  how much it matters. Bopper's real behaviour depends on Compose build contexts and
  `.dockerignore`, which this approximates rather than computes.
- First-parent commits approximate PRs. Direct pushes to the mainline appear as
  one-commit "branches" and are counted as such.
- Nothing here measures memory or startup time per stack. Still owed.
