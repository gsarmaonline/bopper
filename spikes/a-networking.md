# Spike A — overlay containers on the baseline network

**Phase 0. Run on Docker 29.3.1, Compose v5.1.1, macOS.**

Scripts: [`a-networking.sh`](a-networking.sh), [`a2-two-networks.sh`](a2-two-networks.sh),
[`a3-name-order.sh`](a3-name-order.sh). Fixture: [`fixture/compose.yaml`](fixture/compose.yaml),
two `traefik/whoami` services, which echo their own hostname so a response
identifies the container that answered.

## The question

An overlay runs a changed copy of a service that the baseline also runs. What does
Docker DNS do with the name they share?

## Result 1 — a shared alias splits traffic in half

With the baseline stack alone, `orders` resolved to the baseline 20 times out of 20.
Start an overlay that takes the same `orders` alias on the same network, and:

```
10 Hostname: orders-baseline
10 Hostname: orders-overlay
```

Docker returns an A record per container holding the alias, and the client
round-robins. Half the baseline's own traffic lands on the branch's container. This
is silent: nothing fails, nothing logs, the baseline is simply wrong half the time.

Give the overlay a distinct alias instead and the split disappears — `orders` stays
baseline 20/20, `orders-feature-x` is overlay 20/20.

**So an overlay must never take the baseline's alias on the baseline network.**

## Result 2 — the alphabetically first network name wins

A distinct alias solves one service, but not two. If a branch changes `orders` *and*
`payments`, the overlay `orders` must reach the overlay `payments`, not the
baseline's. That needs a second network, and raises a harder question: when a
container sits on two networks that both define `payments`, which answers?

Three theories, and the experiments separate them:

| Experiment | Networks, in creation order | Client joined first | Winner  |
| ---------- | --------------------------- | ------------------- | ------- |
| A2         | `bopper_baseline`, `bopper_ws_featurex` | either          | baseline |
| A3         | `n_zzz`, `n_aaa`            | `n_zzz`             | `n_aaa`  |

A2 rules out "newest network wins" — the older one won. A3 rules out "oldest wins"
and "attachment order wins" — the newer, later-joined network won. A3 also rules out
subnet order, because the winner held the *higher* subnet (172.29 against 172.28).

Only one theory survives both: **Docker resolves across attached networks in
alphabetical order of network name, and returns the first match.** Resolution did not
split in either case; one network answered all 20 requests.

## The rule

Give each workspace its own network, named so it sorts before the baseline's. Attach
overlay containers to both. Attach baseline containers to the baseline network only.

```
bopper_10_ws_<id>     workspace network, sorts first
bopper_90_baseline    baseline network
```

The numeric prefixes are deliberate. Sorting must be obvious to a reader, not an
ASCII-table fact about whether `-` precedes `_`.

This gives fall-through for free:

| Lookup                          | Resolves to | Why                                            |
| ------------------------------- | ----------- | ---------------------------------------------- |
| overlay → a service it changed  | overlay     | The workspace network sorts first and has it   |
| overlay → a service it did not  | baseline    | The workspace network has no such name         |
| baseline → anything             | baseline    | Baseline containers never join a workspace net |

**Overlay containers keep the plain service names.** No per-service aliases, no
rewritten environment, no proxy. `vision.md` assumed the overlay must be "pointed at
baseline hostnames for everything else"; it does not. The resolver does it.

## The catch

This is an implementation detail of the Docker resolver, not a documented guarantee.
It could change in a future version, and the failure mode is the silent 10/10 split
from Result 1 rather than an error.

`a3-name-order.sh` therefore doubles as a regression test and exits non-zero if the
behaviour changes. **Run it after any Docker upgrade, and wire it into CI once CI
exists.** Confirmed on Docker 29.3.1 only.

If it ever breaks, the fallback is the Result 1 answer: distinct aliases per service,
plus rewritten service names in the overlay's environment. That works, costs a
resolution step in `bop up`, and is what the design assumed anyway.

## Still open

- A workspace that changes a service the baseline reaches *first* still needs header
  routing. Fall-through is one-directional, and that is Phase 6.
- Traefik must join every workspace network to route `<id>.localhost`. Untested.
