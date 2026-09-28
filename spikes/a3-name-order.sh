#!/usr/bin/env bash
# Spike A3: what decides which network answers, when a container is attached to
# two networks that both define the same service name?
#
# Three candidate theories:
#   1. attachment order  - the network the container was created with wins
#   2. subnet order      - the lower subnet wins
#   3. name order        - the alphabetically first network name wins
#
# This script flips the name order while holding creation order constant, so a
# flipped winner rules theories 1 and 2 out.
#
# It doubles as a REGRESSION TEST. The fall-through design depends on theory 3,
# which is an implementation detail of the Docker resolver, not a documented
# guarantee. Run this after any Docker upgrade.
#
# Run: ./spikes/a3-name-order.sh   (exit 0 = name order holds)

set -u
N=20

cleanup() {
  docker rm -f bp_c bp_p1 bp_p2 >/dev/null 2>&1
  docker network rm n_aaa n_zzz >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

# Create n_zzz FIRST, so creation order and name order disagree.
docker network create n_zzz >/dev/null
docker network create n_aaa >/dev/null

docker run -d --name bp_p1 --hostname on-ZZZ --network n_zzz \
  --network-alias payments traefik/whoami >/dev/null
docker run -d --name bp_p2 --hostname on-AAA --network n_aaa \
  --network-alias payments traefik/whoami >/dev/null
sleep 1

echo "=== subnets (the winner having the HIGHER subnet rules out subnet order) ==="
for c in bp_p1 bp_p2; do
  docker inspect -f '  {{.Config.Hostname}}: {{range $k,$v := .NetworkSettings.Networks}}{{$k}}={{$v.IPAddress}} {{end}}' "$c"
done

# Attach the client to n_zzz FIRST, so attachment order also disagrees with name order.
docker run -d --name bp_c --network n_zzz curlimages/curl:latest sleep 300 >/dev/null
docker network connect n_aaa bp_c
sleep 1

echo
echo "=== client joined n_zzz first, then n_aaa; resolving 'payments' x$N ==="
OUT=$(docker exec bp_c sh -c \
  "for i in \$(seq 1 $N); do curl -s --max-time 2 http://payments/ | grep '^Hostname:'; done" 2>/dev/null)
echo "$OUT" | sort | uniq -c | sed 's/^/    /'

echo
if [ "$(echo "$OUT" | sort -u | wc -l | tr -d ' ')" != "1" ]; then
  echo "FAIL: resolution split across networks. Fall-through is not deterministic."
  exit 1
elif echo "$OUT" | grep -q "on-AAA"; then
  echo "PASS: the alphabetically first network name won, against both creation"
  echo "      order and attachment order. Name order decides."
  exit 0
else
  echo "FAIL: n_zzz won. Name order no longer decides; the fall-through design"
  echo "      in vision.md needs revisiting."
  exit 1
fi
