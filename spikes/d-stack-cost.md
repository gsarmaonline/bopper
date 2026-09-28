# Measurement — what a stack actually costs

**Phase 0's last debt, collected during Phase 3.** Docker 29.3.1, macOS, 7.6 GiB
allocated to the Docker VM.

This is the number the overlay mechanism is justified by, and it was the one thing
Phase 0 never measured. It is collected here rather than earlier because starting
containers is exactly what Phase 3 does.

## The fixture

Four services: two built from a Dockerfile, plus `postgres:16-alpine` and
`redis:7-alpine`. The two built services are `traefik/whoami`, which is tiny.

## Result

| Measurement                              | Value      |
| ---------------------------------------- | ---------- |
| Full stack, cold start                   | 9 s        |
| Full stack, resident memory              | 36–50 MiB  |
| One overlay container                    | ~2.3 MiB   |
| Shared proxy (Traefik)                   | 22 MiB     |
| `bop up` on an existing workspace, cold proxy | 7 s   |

Baseline plus three overlays against four independent full stacks:

| Arrangement                               | Memory    |
| ----------------------------------------- | --------- |
| Bopper: baseline + 3 overlays + proxy     | 65.3 MiB  |
| Four independent full stacks              | 144.3 MiB |
| **Saving**                                | 78.9 MiB (55%) |

## What this means, honestly

**55% is a real saving, and in absolute terms it is small.** Seventy-nine megabytes
is nothing on a developer laptop. This confirms the reservation raised before Phase 3
began: locally, the overlay mechanism is not a memory story.

Three things distort the figure, and they pull in opposite directions:

- **The fixture's app containers are unrealistically small.** `traefik/whoami` uses
  about 2 MiB; a real Node, Rails or Django service uses 50–150 MiB. With realistic
  app containers the saving grows substantially, because each avoided full stack
  avoids a database *and* several app processes.
- **Postgres dominates this fixture** at 24–42 MiB of the 36–50 MiB total. Sharing one
  database across worktrees is therefore most of the saving here — which is the data
  layer's doing, not the overlay's.
- **The proxy is a fixed 22 MiB**, a third of Bopper's total at three workspaces. It
  amortises: at ten workspaces it is noise, at one it is most of the overhead.

**The startup time is the better local story.** Nine seconds of `compose up` avoided
per worktree, on top of the minutes and gigabytes Phase 1 already saves on
dependencies and cold builds.

**The economics invert on a server.** Twenty preview environments at a few hundred
megabytes each is real money on a VM billed by the gigabyte, where the same twenty on
a laptop would simply go unnoticed. The measurement supports the earlier reading:
overlays are a preview-environment feature that also runs locally, not the reverse.

## Method

```
docker compose -p shop-cold up -d --wait        # timed
docker stats --no-stream                        # summed per project
bop up feat && bop up alpha && bop up beta      # baseline + 3 overlays
```

Memory is resident usage at idle, immediately after start. Under load the numbers
rise and the ratio changes; nothing here measures that.
