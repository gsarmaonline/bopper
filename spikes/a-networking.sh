#!/usr/bin/env bash
# Spike A: how does an overlay container behave on the baseline network?
#
# Question: if the overlay takes the same network alias as the baseline service
# it replaces, what does Docker DNS do with the baseline's own traffic?
#
# Run: ./spikes/a-networking.sh

set -u

FIXTURE="$(cd "$(dirname "$0")/fixture" && pwd)"
NET=bopper_baseline
OVERLAY=bopper-overlay-orders
N=20

cleanup() {
  docker rm -f "$OVERLAY" >/dev/null 2>&1
  docker compose -f "$FIXTURE/compose.yaml" down -v >/dev/null 2>&1
}
trap cleanup EXIT

# Resolve NAME from inside the network N times; print a tally of the hostnames
# that answered.
tally() {
  local name="$1"
  docker run --rm --network "$NET" curlimages/curl:latest sh -c \
    "for i in \$(seq 1 $N); do curl -s --max-time 2 http://$name/ | grep '^Hostname:'; done" \
    2>/dev/null | sort | uniq -c | sed 's/^/    /'
}

echo "=== Setup: baseline stack only ==="
docker compose -f "$FIXTURE/compose.yaml" up -d --wait >/dev/null 2>&1 || {
  echo "FAILED to start the baseline stack"; exit 1; }
docker compose -f "$FIXTURE/compose.yaml" ps --format '    {{.Name}}  {{.Status}}'

echo
echo "=== Control: resolve 'orders' with no overlay present ($N requests) ==="
tally orders

echo
echo "=== Case 1: overlay takes the SAME alias, 'orders' ==="
docker run -d --name "$OVERLAY" --hostname orders-overlay \
  --network "$NET" --network-alias orders traefik/whoami >/dev/null 2>&1
sleep 1
echo "  resolve 'orders' ($N requests):"
tally orders

echo
echo "=== Case 2: overlay takes a DISTINCT alias, 'orders-feature-x' ==="
docker rm -f "$OVERLAY" >/dev/null 2>&1
docker run -d --name "$OVERLAY" --hostname orders-overlay \
  --network "$NET" --network-alias orders-feature-x traefik/whoami >/dev/null 2>&1
sleep 1
echo "  resolve 'orders' ($N requests):"
tally orders
echo "  resolve 'orders-feature-x' ($N requests):"
tally orders-feature-x

echo
echo "=== Done ==="
