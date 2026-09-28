#!/usr/bin/env python3
"""Spike C: how many services does one real branch change?

This is the number the whole design rests on - "most branches change one or two
services" - and it cannot come from a fixture. It needs real git history.

Method: walk first-parent history. On a squash-merge repo each first-parent
commit is one merged PR; on a merge-commit repo each merge is one PR, and git
shows its first-parent diff. Either way, one first-parent commit approximates
one branch's change set.

Map each changed path to a service by directory. The mapping is stated per repo
rather than parsed from compose, because neither repo's compose file carries a
usable per-service build context: immich builds every service from `context: ../`
and penpot ships prebuilt images. A Bopper user would configure this the same way.

Usage: c-history.py <repo-dir> <name> <service>=<prefix> [<service>=<prefix> ...]
"""
import subprocess, sys, collections

def main():
    repo, name = sys.argv[1], sys.argv[2]
    # "svc=prefix", or "svcA|svcB=prefix" for shared code that rebuilds several
    # services. Input hashing flips every service whose build context contains
    # the changed file, so shared directories must map to all of them.
    mapping = []
    for a in sys.argv[3:]:
        svcs, prefix = a.split('=', 1)
        mapping.append((svcs.split('|'), prefix))

    out = subprocess.run(
        ['git', '-C', repo, 'log', '--first-parent', '--name-only',
         '--format=%x00%H', '--no-renames'],
        capture_output=True, text=True, check=True).stdout

    commits, cur, files = [], None, []
    for line in out.split('\n'):
        if line.startswith('\x00'):
            if cur:
                commits.append((cur, files))
            cur, files = line[1:].strip(), []
        elif line.strip():
            files.append(line.strip())
    if cur:
        commits.append((cur, files))

    def services_of(paths):
        s = set()
        for p in paths:
            for svcs, prefix in mapping:
                if p.startswith(prefix):
                    s.update(svcs)
                    break
        return s

    # Only count branches that touch at least one service. A docs-or-CI-only PR
    # would never start a stack, so including them would flatter the result.
    dist = collections.Counter()
    per_service = collections.Counter()
    touching = 0
    for _, paths in commits:
        if not paths:
            continue
        svcs = services_of(paths)
        if not svcs:
            dist['0 (docs/CI only)'] += 1
            continue
        touching += 1
        dist[len(svcs)] += 1
        for s in svcs:
            per_service[s] += 1

    total = sum(v for k, v in dist.items() if isinstance(k, int))
    print(f'=== {name} ===')
    print(f'  first-parent commits analysed: {len(commits)}')
    print(f'  touching >=1 service:          {touching}')
    print(f'  docs/CI only (excluded):       {dist["0 (docs/CI only)"]}')
    names = sorted({x for svcs, _ in mapping for x in svcs})
    print(f'  services defined:              {", ".join(names)}')
    print()
    cum = 0
    for n in sorted(k for k in dist if isinstance(k, int)):
        c = dist[n]
        cum += c
        print(f'    {n} service(s): {c:6d}  {c/total*100:5.1f}%   cumulative {cum/total*100:5.1f}%')
    one_or_two = sum(dist[n] for n in (1, 2) if n in dist)
    print()
    print(f'  ONE service only:    {dist.get(1,0)/total*100:.1f}%')
    print(f'  ONE OR TWO services: {one_or_two/total*100:.1f}%')
    print()

main()
