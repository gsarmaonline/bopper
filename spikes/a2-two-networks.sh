#!/usr/bin/env bash
# Spike A2: a container attached to two networks, where the same service name
# exists on both. Which one answers?
#
# This decides the multi-service overlay design. If the workspace network wins,
# an overlay can use plain service names and fall through to the baseline for
# anything it does not override. If both answer, it cannot.
#
# Run: ./spikes/a2-two-networks.sh

set -u

BASE=bopper_baseline
WS=bopper_ws_featurex
N=20

cleanup() {
  docker rm -f bp_base_payments bp_ws_payments >/dev/null 2>&1
  docker network rm "$BASE" "$WS" >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

docker network create "$BASE" >/dev/null
docker network create "$WS" >/dev/null

# Baseline payments, plain alias, on the baseline network only.
docker run -d --name bp_base_payments --hostname payments-BASELINE \
  --network "$BASE" --network-alias payments traefik/whoami >/dev/null
# Overlay payments, plain alias, on the workspace network only.
docker run -d --name bp_ws_payments --hostname payments-OVERLAY \
  --network "$WS" --network-alias payments traefik/whoami >/dev/null
sleep 1

# Run a client attached to both networks and resolve 'payments'.
# $1 = which network the container is created with (the other is attached after).
probe() {
  local first="$1" second="$2" label="$3"
  docker rm -f bp_client >/dev/null 2>&1
  docker run -d --name bp_client --network "$first" curlimages/curl:latest \
    sleep 300 >/dev/null
  docker network connect "$second" bp_client
  sleep 1
  echo "  $label"
  docker exec bp_client sh -c \
    "for i in \$(seq 1 $N); do curl -s --max-time 2 http://payments/ | grep '^Hostname:'; done" \
    2>/dev/null | sort | uniq -c | sed 's/^/      /'
  echo "      --- what the resolver returns ---"
  docker exec bp_client sh -c "nslookup payments 2>/dev/null | grep -A4 'Name:' | head -8" \
    2>/dev/null | sed 's/^/      /'
  docker rm -f bp_client >/dev/null 2>&1
}

echo "=== 'payments' exists on BOTH networks ==="
echo
probe "$WS"   "$BASE" "created on workspace net, then attached to baseline:"
echo
probe "$BASE" "$WS"   "created on baseline net, then attached to workspace:"

echo
echo "=== Control: 'orders' exists on NEITHER, 'payments' on baseline only ==="
docker rm -f bp_ws_payments >/dev/null 2>&1
sleep 1
probe "$WS" "$BASE" "workspace net has nothing; expect baseline to answer:"

echo
echo "=== Done ==="
