# Bopper

Run one stack, and give each git worktree only the parts it changed.

**Status: working, on Docker Compose.** `bop up` creates a reflinked worktree, works
out which services the branch changed, and starts only those — reachable at
`<id>.localhost` through a shared proxy, with everything else falling through to one
shared baseline stack. Compose is the first backend, not the model; see
[Backends](#backends).
The database is shared read-only, and cloned when the branch's migrations differ.
`bop clean` reclaims what goes idle. `bop headers on` lets a baseline service reach
an overlay. All six phases are built. See [roadmap.md](roadmap.md) for what each one
settled, and [vision.md](vision.md) for the rationale.

![bop in use](docs/demo.gif)

Every figure in the demo is measured, not invented. The disk and time numbers come
from [Spike B](spikes/b-reflink.md); the fall-through and header routing from
[Spike A](spikes/a-networking.md); the stack cost from
[the measurement](spikes/d-stack-cost.md). Higher quality copy:
[docs/demo.mp4](docs/demo.mp4). Source and build instructions: [video/](video/).

## The problem

Git worktrees make several branches cheap to keep checked out. The application stack
for each one is not cheap. Run it per worktree and you get a full copy each time:
every service, database, cache and queue. Compose does this, and so does every other
way of declaring a stack — the duplication is in the model, not the tool.

Most branches change one or two services. Measured across 13,445 real branches of
immich and penpot, about three quarters of the branches that touch any service touch
exactly one ([spikes/c-history.md](spikes/c-history.md)). The rest of each copy
duplicates what already runs for `main`.

## Install

```
git clone https://github.com/gsarmaonline/bopper && cd bopper
make build          # produces bin/bop
make install        # optional; PREFIX=~/.local make install
```

Requirements: Go 1.24 to build, git, and a filesystem with reflink support — APFS on
macOS, or btrfs or XFS on Linux. Docker, and a Compose file, for the environment half;
the workspace half works without either.

## Use

```
$ bop up feature-x
workspace feature-x
  dir     /home/you/myapp-feature-x
  branch  feature-x (from main)
  data    shared, read-only - migrations match the baseline
  env     starting 1 of 4 services...
  overlay orders
  url     http://feature-x.localhost:8080

$ bop status feature-x
feature-x vs baseline (myapp)

SERVICE   CHANGED  BASELINE          WORKSPACE
orders    build    33e60ca063bb563b  8a2dff74283db72c
payments  config   ab7738be1c5b0619  1c9f59e4856ba851

$ bop ps
baseline  running  (bopper_90_baseline)

WORKSPACE  CONTAINERS                     URL
feature-x  bopper-ws-feature-x-orders--…  http://feature-x.localhost:8080

$ bop ls
$ bop clean [-idle 3h] [-remove-after 72h] [-dry-run]
$ bop down feature-x [-delete-branch]
```

### Database flags

By default a workspace reads the baseline's real data through a read-only role, and
gets its own copy only if its migrations differ from the baseline's. Two flags
override that:

```
$ bop up feature-x                # shared read-only, or cloned if migrations differ
$ bop up feature-x -isolate-db    # always its own copy, to throw data away freely
$ bop up feature-x -share-db      # shared WITH writes — affects everyone
```

`-share-db` is the one configuration where a workspace can corrupt the baseline's
data and every other worktree's view of it, so `bop up` says so when you use it.

### Other flags

```
$ bop up feature-x -no-env        # create the worktree; start nothing
$ bop up feature-x -base develop  # fork from a branch other than the current one
$ bop headers on                  # let a baseline service reach an overlay
```

`bop up` also starts the environment: the shared baseline stack once, then only the
changed services on a workspace network that falls through to it. Run `bop up` again
after editing to re-apply. The proxy listens on 8080; set `BOPPER_PROXY_PORT` to
change it.

`bop up` creates the worktree, clones the main working directory into it with
reflinks, and patches `.env`. Dependencies and build outputs come along without a
reinstall, and mtimes are preserved so incremental builds stay warm. On 41k files and
1.2 GB that costs 22 MB and 9 seconds, against 1305 MB and 23–35 seconds for a real
copy ([spikes/b-reflink.md](spikes/b-reflink.md)).

`bop status` hashes each service's build inputs — context files after
`.dockerignore`, the Dockerfile, build args, base images — and its resolved runtime
configuration, then compares them with the baseline. It reports `build` or `config`
so you can tell a rebuild from a restart. No build runs.

## How it will work

Four mechanisms, each sharing by default and copying only on change:

| Layer      | Mechanism                                                                    | Status  |
| ---------- | ---------------------------------------------------------------------------- | ------- |
| Filesystem | Reflink clone of the main working directory                                  | done    |
| Detection  | A hash of each service's build inputs, compared against the baseline         | done    |
| Services   | Changed services only, on a workspace network that falls through to baseline | done    |
| Data       | Shared read-only, cloned only when a migration differs                       | done    |
| Lifecycle  | Everything labelled; idle overlays stopped, orphans reclaimed                | done    |
| Routing    | `X-Worktree` lets a baseline service reach an overlay (opt-in)              | done    |

The services layer rests on a measured property of Docker's resolver: a container
attached to two networks resolves names from the alphabetically first network name,
so a workspace network named to sort before the baseline's gives fall-through for
free ([spikes/a-networking.md](spikes/a-networking.md)).

## Backends

Bopper runs on **Docker Compose** today. That is a fact about the implementation
rather than about the tool: the design separates what a workspace *is* from what
runs it.

| Layer            | Knows about                       | Talks to                    |
| ---------------- | --------------------------------- | --------------------------- |
| Workspace        | git, reflinks, the filesystem     | nothing else                |
| **Boundary**     | **a workspace ID and a directory**|                             |
| Environment      | containers, networks, routing     | one backend                 |

Everything above the environment layer — the CLI, the data layer, change
detection's output — speaks in neutral terms (`internal/stack`), and the backend
contract is one interface (`environment.Backend`). A second backend replaces one
type rather than threading new concepts through the tool.

What a backend has to answer is small: describe the stack in a directory, say which
services a workspace changed, bring up a shared baseline, start a subset of services
and route to them, tear them down, and reclaim what goes idle.

Plausible next ones, in rough order of fit:

- **Podman**, through its Docker-compatible socket. Bopper targets the Compose spec
  and the Docker API rather than Docker internals, so this is close to free.
- **Plain Docker, no Compose file** — a stack declared some other way, or inferred.
- **Kubernetes**, with namespaces for overlays and mirrord-style routing. This is the
  one `vision.md` has always pointed at, and the reason the boundary is where it is.

None of these exist yet. The seam does.

## What Bopper shares, and what it copies

Bopper shares by default, at every layer. It copies in two cases only: when a change
would otherwise corrupt the shared resource, and when you ask for a copy.

| Resource                        | Default                                              | Copy when                                            | Built  |
| ------------------------------- | ---------------------------------------------------- | ---------------------------------------------------- | ------ |
| Services                        | The baseline serves every service                    | The service's build inputs changed                   | yes    |
| Database                        | Shared with the baseline, through a read-only role   | The branch's migrations differ, or you ask           | yes    |
| Queue consumers, scheduled jobs | The baseline's keep running; the overlay starts none | You ask                                              | partly |
| Cache                           | Shared keys, so the overlay starts warm              | You ask                                              | no     |

The database is shared through a read-only role, so a workspace reads the baseline's
real data — no seed, no wait — and **cannot** write to it. A branch whose migrations
differ from the baseline's gets its own database without being asked, because that
migration would otherwise change the schema for every other worktree.

```
bop up feature-x                 # shared read-only, or cloned if migrations differ
bop up feature-x -isolate-db     # always its own database
bop up feature-x -share-db       # shared WITH writes; affects everyone
```

Bopper exports `BOPPER_DB_NAME` and `BOPPER_DB_URL`, and additionally rewrites a
variable your project already uses — `DATABASE_URL`, `DB_NAME`, `POSTGRES_DB` and a
few others — so the common case needs no change to your Compose file. It does not
invent variables your application never reads.

Postgres only for now; MySQL is detected and declined rather than half-supported.
An overlay starts no queue consumer and no scheduled job, because it only runs the
services it was told to, but there is no `-isolate` flag for caches yet.

## What this does not solve yet

- **Header routing needs your services to forward the header.** `bop headers on` puts
  the proxy in front of the baseline so a baseline service can reach an overlay. The
  edge tags the request, but a service that does not forward `X-Worktree` sends the
  next hop to the baseline version, and nothing looks wrong. Propagation is the
  application's job. It is off by default, because it also puts every internal
  baseline call through the proxy.
- **`CREATE DATABASE … TEMPLATE` is a full copy, not a copy-on-write clone.** Bopper
  uses it when it can and falls back to a dump and restore when the baseline has live
  connections. Only a ZFS or btrfs snapshot of the data directory would be genuinely
  cheap, and that is not built.
- **Caches are shared with no isolation option.** A branch that changes a cached
  value's shape can poison a shared key.
- **A branch that changes a queue consumer or a scheduled job needs `--isolate`.**
  Two copies of a singleton compete for the same messages.
- **One branch in five changes every service**, through shared code or a lockfile.
  Those branches save nothing, and `bop up` must fall back to a full stack.
- **Builds must be reproducible enough for input hashes to stay stable.** Base images
  are hashed as written, so a moved tag goes unnoticed; pin them by digest.

## Cleanup

Docker cannot reclaim this usefully on its own: it records no last-access time,
`prune --filter until=` filters on creation time rather than use, and that filter
does not apply to volumes at all. Every resource Bopper creates carries
`bopper.workspace=<id>`, and the proxy writes an access log, so `bop clean` can be
precise:

```
$ bop clean -dry-run
ACTION  KIND       NAME                           WHY
stop    container  bopper-ws-feat-orders--feat-1  idle 4h12m
remove  container  bopper-ws-beta-orders--beta-1  workspace no longer exists
```

Stopping frees memory and leaves data and the worktree alone. A workspace whose
worktree is gone is reclaimed regardless of age.

## Development

```
make            # fmt, vet, test, build
make test
make spikes     # re-run the Docker DNS regression test after a Docker upgrade
```

The Phase 0 experiments live in [spikes/](spikes/), each with its script and its
findings. They are worth reading before changing the mechanisms they measured.

## Documents

- [roadmap.md](roadmap.md) — the six build phases, their exit tests, and the open
  questions that block them.
- [vision.md](vision.md) — problem analysis, prior art, architecture, hard edges,
  and the product direction beyond local development.

## License

MIT. See [LICENSE](LICENSE).
