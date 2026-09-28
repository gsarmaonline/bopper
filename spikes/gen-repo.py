#!/usr/bin/env python3
"""Generate a repository that behaves like a real checkout for Spike B.

Spike B measures mechanics: does a reflink clone cost disk, do mtimes survive,
does git see cloned files correctly, does an incremental build stay warm. Those
properties depend on file count, file sizes and directory shape - not on whether
the bytes are real npm packages. So a generated tree measures them honestly, and
lets us sweep sizes that one real repo cannot.

Shape mimics a JS monorepo: a small tracked source tree, and a large ignored
dependency tree of many small files in deep directories.

Usage: gen-repo.py <dir> --deps-files N --deps-mb M --src-files S
"""
import argparse, os, random, subprocess, sys

def write(path, nbytes, rng):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    # Incompressible-ish content, so APFS cannot dedupe it away and confuse the
    # free-space measurement.
    with open(path, 'wb') as f:
        f.write(rng.randbytes(nbytes))

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('dir')
    ap.add_argument('--deps-files', type=int, default=40000)
    ap.add_argument('--deps-mb', type=int, default=1200)
    ap.add_argument('--src-files', type=int, default=600)
    a = ap.parse_args()

    rng = random.Random(1729)
    root = os.path.abspath(a.dir)
    os.makedirs(root, exist_ok=True)

    # Tracked source, laid out as services so it doubles as a Compose-shaped repo.
    services = ['orders', 'payments', 'gateway', 'workers']
    per = max(1, a.src_files // len(services))
    for svc in services:
        for i in range(per):
            write(os.path.join(root, svc, 'src', f'mod_{i:04d}.ts'),
                  rng.randint(800, 6000), rng)
        write(os.path.join(root, svc, 'Dockerfile'), 200, rng)

    # Ignored dependency tree: many small files, deep nesting, a few big ones.
    total = a.deps_mb * 1024 * 1024
    n = a.deps_files
    # 90% of files are small, and carry 40% of the bytes; the rest are chunkier.
    small_n = int(n * 0.9)
    small_total = int(total * 0.4)
    small_avg = max(64, small_total // max(1, small_n))
    big_n = n - small_n
    big_avg = max(1024, (total - small_total) // max(1, big_n))

    for i in range(n):
        pkg = f'pkg_{i % 900:03d}'
        sub = f'lib/{(i // 900) % 12}/deep'
        size = small_avg if i < small_n else big_avg
        size = max(32, int(size * rng.uniform(0.5, 1.5)))
        write(os.path.join(root, 'node_modules', pkg, sub, f'f_{i:06d}.js'), size, rng)

    with open(os.path.join(root, '.gitignore'), 'w') as f:
        f.write('node_modules/\n')

    subprocess.run(['git', 'init', '-q', '-b', 'main'], cwd=root, check=True)
    subprocess.run(['git', 'add', '-A'], cwd=root, check=True)
    subprocess.run(['git', '-c', 'user.name=spike', '-c', 'user.email=spike@local',
                    'commit', '-q', '-m', 'generated baseline'], cwd=root, check=True)
    print(f'generated {root}')

main()
