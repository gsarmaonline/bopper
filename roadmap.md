# Bopper roadmap

Six phases. The order follows risk, not feature value. The two assumptions that could
sink the design get tested before anything is built.

All six phases are done. What each one settled, and what it cost, is below.

| Phase | Delivers                              | Exit test                                                      | Status |
| ----- | ------------------------------------- | -------------------------------------------------------------- | ------ |
| 0     | Three spikes and a measured baseline  | Spikes documented; the numbers recorded                        | DONE   |
| 1     | Workspace layer: `bop up` / `bop down` | Workspaces create and destroy reliably                         | DONE   |
| 2     | Change detection: `bop status`         | No false negatives on a corpus of real changes                 | DONE   |
| 3     | Partial stack, no header routing      | A one-service change starts one container; the app works       | DONE   |
| 4     | Data: read-only default, clone on need | A schema branch clones, a code branch shares, neither breaks   | DONE   |
| 5     | Lifecycle: labels, `bop ls`, `bop clean` | Ten workspaces left a week do not fill the disk              | DONE   |
| 6     | Header routing                        | A non-edge service receives tagged traffic                     | DONE   |

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

**Measurement. DONE.** See [spikes/d-stack-cost.md](spikes/d-stack-cost.md): a
four-service stack costs 36-50 MiB and 9 s cold; baseline plus three overlays against
four full stacks saves 55%, which is only 79 MiB. A real proportion and a small
absolute number on a laptop, which supports reading overlays as a
preview-environment feature that also runs locally.

**Earlier measurement notes.** Spike B measured disk and time per worktree: 22 MB and
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

## Phase 3 — Partial stack. DONE

`bop up` starts the baseline once, then runs only the services the branch changed.
`bop ps` shows what is running; `bop down` removes it. `internal/environment` holds
it, and imports no git.

**Verified against real containers.** A one-service change started one container. The
baseline network resolved `orders` to the baseline's container 20 times out of 20,
while the workspace network resolved it to the overlay's, and `payments` fell through
to the baseline. Three distinct container IDs, so the isolation is measured rather
than assumed.

**Two design decisions worth keeping:**

- **The overlay renames its compose service key** to `<service>--<id>`. Compose adds
  the service key as a network alias on *every* attached network, so a key of
  `orders` would answer to `orders` on the baseline network and split the baseline's
  own traffic 10/10. The plain name is restored as an explicit alias on the workspace
  network only.
- **The proxy uses Traefik's FILE provider, not the Docker provider.** The Docker
  provider would have Traefik rediscover, through the Docker socket, what Bopper
  already knows for certain. It also fails outright wherever socket sharing is
  disabled — which it was on the development machine, where no container could read
  the socket at all. A dynamic config file needs no socket and hands the proxy no
  control of the daemon.

**Four defects the build surfaced:**

- Passing any `-f` to compose suppresses its own file discovery, so the baseline came
  up with "no service selected" until its compose file was named explicitly.
- A `depends_on` whose every entry falls through to the baseline must have the key
  REMOVED. Setting it to null makes compose reject the file.
- Both networks must be `external`. Bopper pre-creates the workspace network so the
  proxy can join it, and compose refuses to adopt a network it did not create.
- `bop up` on an existing workspace used to error. It now re-applies, because that is
  the command a developer reaches for after editing code.

**One caveat.** Traefik's file watch is unreliable on Docker Desktop, where the mount
crosses a VM boundary and inotify events for new files are dropped. Observed directly:
a route file written seconds after the proxy started was never picked up and every
request 404'd. Bopper restarts the proxy when a route file actually changes, and skips
the restart when it has not.

**Exit met:** on a real stack, a one-service change starts one container, the app is
reachable at `http://<id>.localhost:8080`, and teardown leaves no overlay container,
network, route file, worktree or branch behind.

## Phase 4 — Data. DONE

`internal/data`, wired into `bop up` ahead of the overlay — the ordering is
load-bearing, because the data step rewrites `.env` and compose reads `.env` when the
overlay starts.

**The read-only role is the whole safety argument.** Sharing the baseline's real data
is what most branches want, and a `GRANT` makes it provably safe with no proxy, no
parser and no statement inspection. Verified against a live database: `SELECT`
succeeds, `INSERT` returns *permission denied for table orders*, `CREATE TABLE`
returns *permission denied for schema public*.

`ALTER DEFAULT PRIVILEGES` matters as much as the grant itself. Without it a table
created later is invisible to the role, and a workspace fails at runtime on exactly
the tables a colleague just added.

**Cloning** triggers on a migration-directory difference, checked across the
conventional locations. Verified: a branch adding `002_add_col.sql` got
`bopper_schema` carrying the baseline's rows, and running its migration there left the
baseline untouched — clone `id,total,note` against baseline `id,total`.

`CREATE DATABASE … TEMPLATE` is tried first and falls back to a dump and restore,
because TEMPLATE requires that nothing is connected to the source and a running
baseline usually violates that. **TEMPLATE is a full file copy, not a copy-on-write
clone** — `vision.md` conflated the two, and only a ZFS or btrfs snapshot would be
genuinely cheap. That path is not built.

**Three defects the build surfaced:**

- A running container is not a ready database. Postgres reports running well before
  it accepts connections, and SQL in that window fails with a missing socket, which
  reads like a configuration error and is not one. `pg_isready` is the real question.
- SQL must not travel through a shell. `DO $$ … $$` arrived as `DO 81 …` because the
  shell expanded `$$` to its own PID. The password is now passed as a container
  environment variable and no shell is involved.
- `-share-db` fell through to the read-only branch, so the one flag whose whole
  purpose is granting writes was silently connecting read-only.

**Exit met:** a schema-changing branch gets a clone, a code-only branch shares the
baseline read-only, neither breaks the other, and `bop down` drops the clone.

## Phase 5 — Lifecycle. DONE

`bop clean` in `internal/environment/cleanup.go`. Every overlay container carries
`bopper.workspace=<id>`, so cleanup touches only what Bopper made and never the
baseline or another project.

**The proxy writes a JSON access log**, and that is what makes the policy honest.
Docker records no last-access time for anything; `docker system prune --filter until=`
filters on CREATION time, not use, and does not apply to volumes at all. Without the
access log the only signal is a container's start time, which says nothing about
whether anyone has touched the workspace since. Where a workspace has no routed
service and so never appears in the log, Bopper falls back to start time and says so
rather than implying more.

Tiered, as designed: `-idle` stops an overlay and frees memory while leaving data and
the worktree alone; `-remove-after` removes containers and the network. A workspace
whose worktree no longer exists is an orphan and goes regardless of age, which is the
common case — somebody deleted a directory by hand.

**Exit met:** an idle overlay is stopped but kept; a hand-deleted worktree has its
container, network and route file all reclaimed; `-dry-run` reports without changing.

## Phase 6 — Header routing. DONE

`bop headers on` in `internal/environment/headers.go`. This is what lets a BASELINE
service reach an overlay, so a changed service no longer has to sit at the edge of the
call graph.

**Verified against real containers.** On the baseline network, an untagged request for
`orders` reached the baseline 6 times out of 6; the same request carrying
`X-Worktree: feat` reached the overlay 6 out of 6; an unknown workspace tag fell back
to the baseline. The edge injects the header itself, so a hostname is the whole
interface — no browser extension, no curl flag.

**How it works, and why it is opt-in.** For the proxy to route by header it must be
the thing that answers to `orders` on the baseline network. Spike A settled what
happens when two containers share one alias, so under this mode the baseline's own
services give up their plain names too, exactly as the overlay does: they run as
`<service>--base` and the proxy holds the plain name alone.

That is a real cost. Every internal call in the baseline now crosses the proxy, which
adds latency and a single point of failure, and it buys nothing for a branch whose
changed service is already at the edge. So the no-routing mode stays the default and
stays first-class, as `vision.md` requires.

**The limit Bopper cannot fix.** The proxy tags a request at the edge, but a baseline
service that does not FORWARD the header sends the next hop to the baseline version —
and nothing appears to be wrong. Propagation is the application's job, through
OpenTelemetry baggage or explicit forwarding. `bop headers on` says this plainly
rather than letting it be discovered.

**One defect worth recording.** Router names share a single namespace across every
file the provider loads. The edge router and the intercept router were both
`<id>-<service>`, so the intercept file silently overwrote the edge router: header
routed calls worked perfectly while every request to `<id>.localhost` returned 404. A
test now fails if the two namespaces ever collide again.

**Exit met:** a changed service that does not sit at the edge of the call graph
receives tagged traffic.



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
