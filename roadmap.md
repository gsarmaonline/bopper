# Bopper roadmap

Six phases. The order follows risk, not feature value. The two assumptions that could
sink the design get tested before anything is built.

Phases 0 to 3 are the product. Phases 4 to 6 are follow-ons.

| Phase | Delivers                              | Exit test                                                      |
| ----- | ------------------------------------- | -------------------------------------------------------------- |
| 0     | Two spikes and a measured baseline    | Both spikes documented; the numbers recorded                   |
| 1     | Workspace layer: `bop up` / `bop down` | Workspaces create and destroy reliably                         |
| 2     | Change detection: `bop status`         | No false negatives on a corpus of real changes                 |
| 3     | Partial stack, no header routing      | A one-service change starts one container; the app works       |
| 4     | Data: read-only default, clone on need | A schema branch clones, a code branch shares, neither breaks   |
| 5     | Lifecycle: labels, `bop ls`, `bop clean` | Ten workspaces left a week do not fill the disk              |
| 6     | Header routing                        | A non-edge service receives tagged traffic                     |

## Phase 0 — Prove the primitives by hand

No code. Two spikes and a measurement, on a real Compose project.

**Spike A, networking. DONE.** See [spikes/a-networking.md](spikes/a-networking.md).
A shared alias splits baseline traffic 10/10 between baseline and overlay, silently.
A per-workspace network named to sort before the baseline's gives clean fall-through
instead, because Docker resolves across attached networks in alphabetical order of
network name. Overlays keep plain service names. The behaviour is undocumented, so
`spikes/a3-name-order.sh` guards it as a regression test.

**Spike B, filesystem.** Run `git worktree add`, `cp -c`, then `git restore`. Check
that mtimes survive, that an incremental build stays warm, and that git does not
report a cloned file as clean when it differs. A cloned index with cloned mtimes is
the risk. Output is the exact sequence of operations, which
[vision.md](vision.md) does not yet define.

**Measurement.** Record services per stack, memory per stack, startup time, disk per
worktree, and how often a branch changes only one or two services. The design sells
cost reduction and measures nothing today. Every later claim rests on these numbers.

**Exit:** both spikes documented and the numbers recorded. If either spike fails, the
design changes before any code exists.

## Phase 1 — Workspace layer

`bop up` and `bop down` that touch git and the filesystem only. No containers. Create
the worktree, reflink-clone the working directory and its ignored artifacts, patch
`.env` with the workspace ID and hostname, write metadata, and remove it all cleanly.

This is the workspace half of the two-layer split. It outputs a workspace ID and a
directory, and it never calls Docker.

**Exit:** workspaces create and destroy reliably, with disk use measured against a
plain `git worktree add`.

## Phase 2 — Change detection

`bop status` prints which services changed against the baseline, by input hash. No
orchestration. The work sits in defining the inputs correctly: the context files that
survive `.dockerignore`, the Dockerfile, the build args, the base image digest and the
resolved Compose fragment.

**Exit:** no false negatives on a corpus of real changes. A lockfile edit must flip
every service that depends on it.

**Phases 1 and 2 are independent and can run in parallel.** Detection needs two
directories and a Compose file. It needs nothing from the workspace layer. This is the
first test of the narrow boundary between the two layers, and it is worth confirming
that the boundary holds.

## Phase 3 — Partial stack

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
| In what order do `git worktree add`, the reflink clone and `git restore` run? | Phase 1 |
| Which build inputs enter the hash, and which are safe to omit?      | Phase 2 |
| Where does the idle template database come from, and who seeds it?  | Phase 4 |
| What does a stack actually cost today, in memory, disk and seconds? | Phase 0 |
