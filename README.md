# Bopper

Run one Docker Compose stack, and give each git worktree only the containers it changed.

**Status: working.** `bop up` creates a reflinked worktree, works out which services
the branch changed, and starts only those — reachable at `<id>.localhost` through a
shared proxy, with everything else falling through to one shared baseline stack.
`bop clean` reclaims what goes idle. `bop headers on` lets a baseline service reach
an overlay. The data layer is the one piece still unbuilt: databases are shared as
they are, with no clone on a differing migration (Phase 4). See
[roadmap.md](roadmap.md) for the phases, and [vision.md](vision.md) for the
rationale.

![bop in use](docs/demo.gif)

Every figure in the demo is measured, not invented. The disk and time numbers come
from [Spike B](spikes/b-reflink.md); the fall-through and header routing from
[Spike A](spikes/a-networking.md); the stack cost from
[the measurement](spikes/d-stack-cost.md). Higher quality copy:
[docs/demo.mp4](docs/demo.mp4). Source and build instructions: [video/](video/).

## The problem

Git worktrees make several branches cheap to keep checked out. The application stack
for each one is not cheap. Docker Compose starts a full copy per worktree: every
service, database, cache and queue.

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
macOS, or btrfs or XFS on Linux. Docker is not needed yet.

## Use

```
$ bop up feature-x
workspace feature-x
  dir    /home/you/myapp-feature-x
  branch feature-x (from main)
  host   feature-x.localhost  (not serving yet - Phase 3)

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

`bop up` also starts the environment: the shared baseline stack once, then only the
changed services on a workspace network that falls through to it. Run `bop up` again
after editing to re-apply. Pass `-no-env` to create the worktree without containers.
The proxy listens on 8080; set `BOPPER_PROXY_PORT` to change it.

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
| Data       | Shared read-only, cloned only when a migration differs                       | Phase 4 |
| Lifecycle  | Everything labelled; idle overlays stopped, orphans reclaimed                | done    |
| Routing    | `X-Worktree` lets a baseline service reach an overlay (opt-in)              | done    |

The services layer rests on a measured property of Docker's resolver: a container
attached to two networks resolves names from the alphabetically first network name,
so a workspace network named to sort before the baseline's gives fall-through for
free ([spikes/a-networking.md](spikes/a-networking.md)).

## What Bopper shares, and what it copies

Bopper shares by default, at every layer. It copies in two cases only: when a change
would otherwise corrupt the shared resource, and when you ask for a copy.

| Resource                        | Default                                              | Copy when                                            |
| ------------------------------- | ---------------------------------------------------- | ---------------------------------------------------- |
| Services                        | The baseline serves every service                    | The service's build inputs changed                   |
| Database                        | Shared with the baseline, through a read-only role   | The branch changes a migration or writes, or you ask |
| Cache                           | Shared keys, so the overlay starts warm              | You ask                                              |
| Queue consumers, scheduled jobs | The baseline's keep running; the overlay starts none | You ask                                              |

The database is shared through a read-only role, so one worktree cannot corrupt
another's data by accident. A branch that needs writes says so and gets a clone, and
a branch whose migrations differ from the baseline's gets one without asking, because
that migration would otherwise break every other worktree. Pass `--share db` to
override and accept the risk. Everything else stays shared until `--isolate
cache,queues` says otherwise.

## What this does not solve yet

- **Header routing needs your services to forward the header.** `bop headers on` puts
  the proxy in front of the baseline so a baseline service can reach an overlay. The
  edge tags the request, but a service that does not forward `X-Worktree` sends the
  next hop to the baseline version, and nothing looks wrong. Propagation is the
  application's job. It is off by default, because it also puts every internal
  baseline call through the proxy.
- **Databases are shared as they are.** Nothing clones on a differing migration yet,
  so a branch that changes a migration will change it for everyone (Phase 4).
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
