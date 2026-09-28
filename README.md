# Bopper

Run one Docker Compose stack, and give each git worktree only the containers it changed.

**Status: working for the no-routing case.** `bop up` creates a reflinked worktree,
works out which services the branch changed, and starts only those — reachable at
`<id>.localhost` through a shared proxy, with everything else falling through to one
shared baseline stack. Databases are still shared as-is (Phase 4) and a baseline
service cannot yet call an overlay (Phase 6). See [roadmap.md](roadmap.md) for the
phases, and [vision.md](vision.md) for the rationale.

![bop in use](docs/demo.gif)

Every figure in the demo is measured, not invented. The disk and time numbers come
from [Spike B](spikes/b-reflink.md); the fall-through behaviour comes from
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

- **A baseline service cannot call an overlay service.** Header routing is Phase 6.
  Until then the changed service must sit at the edge of the call graph.
- **A branch that changes a queue consumer or a scheduled job needs `--isolate`.**
  Two copies of a singleton compete for the same messages.
- **One branch in five changes every service**, through shared code or a lockfile.
  Those branches save nothing, and `bop up` must fall back to a full stack.
- **Builds must be reproducible enough for input hashes to stay stable.** Base images
  are hashed as written, so a moved tag goes unnoticed; pin them by digest.

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
