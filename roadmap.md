# Bopper roadmap

Six phases. The order follows risk, not feature value. The two assumptions that could
sink the design get tested before anything is built.

Phases 0 to 3 are the product. Phases 4 to 6 are follow-ons.

| Phase | Delivers                              | Exit test                                                      | Status |
| ----- | ------------------------------------- | -------------------------------------------------------------- | ------ |
| 0     | Three spikes and a measured baseline  | Spikes documented; the numbers recorded                        | DONE   |
| 1     | Workspace layer: `bop up` / `bop down` | Workspaces create and destroy reliably                         | DONE   |
| 2     | Change detection: `bop status`         | No false negatives on a corpus of real changes                 | DONE   |
| 3     | Partial stack, no header routing      | A one-service change starts one container; the app works       | next   |
| 4     | Data: read-only default, clone on need | A schema branch clones, a code branch shares, neither breaks   |        |
| 5     | Lifecycle: labels, `bop ls`, `bop clean` | Ten workspaces left a week do not fill the disk              |        |
| 6     | Header routing                        | A non-edge service receives tagged traffic                     |        |

## Phase 0 — Prove the primitives by hand

No code. Two spikes and a measurement, on a real Compose project.

**Spike A, networking. DONE.** See [spikes/a-networking.md](spikes/a-networking.md).
A shared alias splits baseline traffic 10/10 between baseline and overlay, silently.
A per-workspace network named to sort before the baseline's gives clean fall-through
instead, because Docker resolves across attached networks in alphabetical order of
network name. Overlays keep plain service names. The behaviour is undocumented, so
`spikes/a3-name-order.sh` guards it as a regression test.

**Spike B, filesystem. DONE.** See [spikes/b-reflink.md](spikes/b-reflink.md).
On 41k files and 1.2 GB, a reflink clone costs 22 MB against 1305 MB for a real copy,
and 9 s against 23-35 s. The sequence is `git worktree add`, then overlay the main
working directory with `cp -c` skipping `.git`, then `git checkout -- .`. Mtimes
survive every step, the incremental build stays warm, and git detects a cloned file
whose content differs even at identical size and mtime, because the index compares
inode and ctime too.

**Measurement. MOSTLY DONE.** Spike B measured disk and time per worktree: 22 MB and
9 s against 1305 MB and 23-35 s for a real copy, on a 1.2 GB tree.
[Spike C](spikes/c-history.md) measured the premise against real history from immich
and penpot, 13,445 branches: about three quarters of service-touching branches change
exactly one service, and 79-84% change at most two. Memory per stack and startup time
are still unmeasured.

**Spike C, history. DONE.** See [spikes/c-history.md](spikes/c-history.md). The
premise holds, with a caveat the design must handle: 16-21% of branches touch three or
more services, clustered on shared code and lockfile changes. `bop up` must degrade to
a full stack gracefully, because that is not an edge case at one in five branches.

**Exit:** both spikes documented and the numbers recorded. If either spike fails, the
design changes before any code exists.

## Phase 1 — Workspace layer. DONE

`bop up`, `bop down` and `bop ls`, in `internal/workspace`, `internal/git`,
`internal/clone` and `internal/envfile`. Git and the filesystem only; nothing here
imports Docker.

State lives in the shared `.git` directory, never in the working tree, so it can
neither appear as an untracked file nor be cloned into a workspace. Directories are
removed only through `git worktree remove`, which refuses any path git does not
already track as a worktree - Bopper never deletes a directory by path.

Two defects the tests now guard:

- `filepath.Join(src, ".")` cleans away the trailing `/.`, turning the merging copy
  into a nesting one that produced `payments/payments/`. The overlay must build that
  path by string concatenation.
- `Overlay` assumed its destination existed. It does in the `bop up` flow, but not in
  general.

**Exit met:** workspaces create and destroy reliably, ignored artifacts arrive without
a reinstall, `.git` survives as the worktree's own file, the baseline's `.env` is
untouched, and a hand-deleted worktree does not wedge the next `bop up`.

## Phase 2 — Change detection. DONE

`bop status` prints which services a workspace changes, by input hash, in
`internal/detect`. No build runs. It distinguishes `build` from `config`, so a
rebuild is visibly different from a restart.

Hashed: the context files that survive `.dockerignore`, the Dockerfile, its `FROM`
lines, the build args and target, and the resolved runtime configuration.

Three normalizations were needed, and each was found by a test that failed:

- **Absolute paths.** A workspace lives elsewhere, so its volume sources all differ.
  The project directory is rewritten to a placeholder before hashing.
- **Workspace identity variables.** `BOPPER_WORKSPACE` and friends are replaced with
  fixed sentinels *before* interpolation, on both sides. Normalizing afterwards does
  not work: `${BOPPER_WORKSPACE:-baseline}` resolves to the ID in a workspace and to
  the literal `baseline` in the baseline, so rewriting the ID leaves two different
  strings.
- **Bopper's own `.env` block.** A build context usually contains `.env`, and Bopper
  patched it. The managed block is stripped before hashing, with trailing whitespace
  trimmed on both paths - trimming only when a marker was found made `FOO=bar` differ
  from `FOO=bar\n`.

**Exit met:** the test corpus covers both Compose patterns. With per-service contexts
a source change flips exactly one service; with a monorepo `context: .` a lockfile
change flips every service, which is correct and is why Spike C's honest numbers are
worse than its flattering ones. `.dockerignore`d files are not inputs, and two
identical checkouts report no change at all.

**Phases 1 and 2 were independent and were built in parallel**, as predicted. Detection
needs two directories and a Compose file and nothing from the workspace layer. The
narrow boundary held; the only coupling that emerged was the `.env` marker, which now
lives in its own package that both sides import.

## Phase 3 — Partial stack. NEXT

The first end-to-end useful version. Start only the changed services, on the baseline
network, under the naming rule from Phase 0, pointed at baseline hostnames for
everything else, and reachable at `<id>.localhost` through a shared Traefik.

No header routing. The overlay runs no queue consumer and no scheduled job, because a
second copy of a singleton competes with the baseline's.

**Exit:** on a real project, a one-service change starts one container and the app
works at the workspace URL.

## Phase 4 — Data

A read-only role on the baseline database as the default, which makes sharing provably
safe with no proxy and no parser. Detection of differing migrations, and a clone for
those branches. Both clone paths: a `CREATE DATABASE … TEMPLATE` copy from an idle
template database, and a ZFS or btrfs snapshot. The `--share db` and `--isolate`
overrides.

**Exit:** a schema-changing branch gets a clone, a code-only branch shares the
baseline, and neither breaks the other.

## Phase 5 — Lifecycle

A `bopper.workspace=<id>` label on every overlay container, volume and database clone.
`bop ls` and `bop clean`. The tiered idle policy: stop overlays after hours, delete
data only after days or after the worktree is gone. Traefik access logs as the real
last-use signal, which Docker does not record.

**Exit:** ten workspaces left for a week do not fill the disk.

## Phase 6 — Header routing

`X-Worktree` propagation, hostname-to-header conversion at the edge, and
OpenTelemetry baggage for the hops in between. This is what lets a baseline service
call an overlay.

**Exit:** a changed service that does not sit at the edge of the call graph receives
tagged traffic.

## Decisions

### Language: Go

One argument decides it, and it is not about the language itself. `compose-go` is the
official Compose spec loader, and `docker compose` uses it. It handles interpolation,
`extends`, multi-file merge semantics, profiles, variable defaults and `depends_on`
conditions. Rust has no equivalent of comparable fidelity.

That matters more here than in most projects. Bopper's premise is an unchanged Compose
file. A tool that disagrees with `docker compose` about what the stack is, because its
own parser gets `extends` or a merge rule subtly wrong, fails at the premise rather
than at the edges. The Docker API client and the BuildKit client are Go for the same
reason, and Traefik is Go as well.

The work also has the wrong shape for Rust. Bopper shells out to git, clones files,
calls the Docker socket and waits on builds. It is I/O-bound glue, and its own runtime
is noise next to a container build. Both languages produce a single static binary, so
Rust's usual deployment advantage is neutral here.

**The counter-argument, recorded so nobody re-derives it.** `docker compose config
--format json` hands any language a fully resolved spec, which weakens the
`compose-go` argument. It costs a subprocess per call and still leaves you to model
the result. The Docker API and BuildKit points survive it.

**Where Rust wins.** The lazy database clone in the product section needs a
protocol-aware proxy, which is Rust's domain; pgcat is Rust for good reason. Build
that as a separate binary if it happens. It does not drag the CLI with it.

**What would reverse this.** A team that writes Rust and not Go. Fluency beats library
fit, and `bollard` plus `docker compose config` is a workable path.

## Open questions

These block the phases named beside them.

| Question                                                            | Blocks  |
| ------------------------------------------------------------------- | ------- |
| Should a lockfile change really rebuild every service, or should the closure be per-service? | Phase 2 revisit |
| Where does the idle template database come from, and who seeds it?  | Phase 4 |
| What does a stack cost in memory and startup seconds?               | Phase 0 |
