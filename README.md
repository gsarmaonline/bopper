# Bopper

Run one Docker Compose stack, and give each git worktree only the containers it changed.

**Status: design proposal. No code exists yet.** This document describes the intended
tool. The commands below do not run today. For the full rationale, the prior-art
survey and the product direction, read [vision.md](vision.md).

## The problem

Git worktrees make several branches cheap to keep checked out. The application stack
for each one is not cheap. Docker Compose starts a full copy per worktree: every
service, database, cache and queue.

Most branches change one or two services. The rest of each copy duplicates what
already runs for `main`. The cost appears as startup time, memory, disk, and as
friction that stops you from running more than one or two stacks.

## The idea

Run one full stack from `main`. For each worktree, start only the services that
changed, and let everything else fall through to that shared baseline.

Four mechanisms make each layer share by default and copy only on change:

| Layer      | Mechanism                                                                       |
| ---------- | ------------------------------------------------------------------------------- |
| Filesystem | Reflink clone of the main working directory (`cp -c` on APFS, `--reflink` on btrfs and XFS) |
| Detection  | A hash of each service's build inputs, compared against the baseline            |
| Services   | Changed services only, joined to the baseline network behind a shared Traefik    |
| Data       | A database clone from a seeded template, for worktrees that change the schema    |

## Planned commands

```
bop up feature-x      # create the worktree, detect changes, start the overlay
bop ls                # list workspaces and their running services
bop down feature-x    # stop the overlay, drop clones, remove the worktree
bop clean             # reclaim idle overlays, volumes and database clones
```

`bop up` prints a URL. The workspace becomes reachable at `feature-x.localhost`
through the shared Traefik.

## Requirements

- A filesystem with reflink support: APFS on macOS, or btrfs or XFS on Linux.
- Docker with BuildKit, or Podman through its Docker-compatible socket.
- A Compose file for the project.
- A shared Traefik instance, which Bopper starts if none runs.

## What Bopper shares, and what it copies

Bopper shares by default, at every layer. It copies in two cases only: when a change
would otherwise corrupt the shared resource, and when you ask for a copy.

| Resource                        | Default                                              | Copy when                                        |
| ------------------------------- | ---------------------------------------------------- | ------------------------------------------------ |
| Services                        | The baseline serves every service                    | The service's build inputs changed               |
| Database                        | Shared with the baseline, through a read-only role   | The branch changes a migration or writes, or you ask |
| Cache                           | Shared keys, so the overlay starts warm              | You ask                                          |
| Queue consumers, scheduled jobs | The baseline's keep running; the overlay starts none | You ask                                          |

The database is shared through a read-only role, so one worktree cannot corrupt
another's data by accident. A branch that needs writes says so and gets a clone, and
a branch whose migrations differ from the baseline's gets one without asking, because
that migration would otherwise break every other worktree. Pass `--share db` to
override and accept the risk. Everything else stays shared until `--isolate
cache,queues` says otherwise.

## What this does not solve yet

- **A baseline service cannot call an overlay service.** The first version drops
  header routing. It covers the common case, where the changed service sits at the
  edge of the call graph. See "Hard edges" in [vision.md](vision.md).
- **A branch that changes a queue consumer or a scheduled job needs `--isolate`.**
  Two copies of a singleton compete for the same messages, so Bopper runs neither
  copy in the overlay until you scope it.
- **Builds must be reproducible enough for input hashes to stay stable.**

## Documents

- [roadmap.md](roadmap.md) — the six build phases, their exit tests, and the open
  questions that block them.
- [vision.md](vision.md) — problem analysis, prior art, architecture, hard edges,
  and the product direction beyond local development.

## License

MIT. See [LICENSE](LICENSE).
