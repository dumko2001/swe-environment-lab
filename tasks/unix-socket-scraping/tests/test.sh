#!/bin/sh
set -eu

mkdir -p /logs/verifier
work=/tmp/candidate
candidate=/tmp/micro1-prometheus
controller=/tmp/micro1-uds-controller
binary=/tmp/micro1-uds-pool-tests
runtime=/tmp/micro1-runtime
proof=/tmp/micro1-uds-proof

if /tests/common/prepare-candidate.sh /app/verifier-artifacts/candidate.tar.gz /app/verifier-artifacts/candidate.sha256 "$work"; then
  :
else
  code=$?
  test "$code" -ne 20 || exit "$code"
  printf '0\n' > /logs/verifier/reward.txt
  exit 0
fi

cp /tests/hidden_pool_identity_test.go "$work/prometheus/scrape/micro1_pool_identity_test.go"
cp /tests/hidden_client_retirement_test.go "$work/prometheus/scrape/micro1_client_retirement_test.go"
cp /tests/hidden_transport_marker_test.go "$work/prometheus/scrape/micro1_transport_marker_test.go"
# Assertions and test-only helpers must not resolve through submitted replacements.
for helper_path in promql/promqltest util/testutil util/teststorage; do
  rm -rf "$work/prometheus/$helper_path"
  cp -R "/opt/reference/source/$helper_path" "$work/prometheus/$helper_path"
done
/tests/isolate-test-helpers /usr/local/go/bin/go /opt/reference/source "$work/prometheus"
if ! /tests/common/hardened-run.sh compile "$work/prometheus" scrape scrape "$binary" "$proof" \
  || ! /tests/common/hardened-run.sh compile-program "$work/prometheus" ./cmd/prometheus "$candidate" \
  || ! /tests/common/hardened-run.sh compile-controller /tests/controller.go "$controller"; then
  printf '0\n' > /logs/verifier/reward.txt
  printf '%s\n' '{"phase":"build","status":"candidate_build_failed","reward":0}' > /logs/verifier/status.json
  exit 0
fi
/tests/common/hardened-run.sh seal "$work/prometheus" "$runtime" scrape

if /tests/common/hardened-run.sh run-controller "$controller" "$runtime" "$candidate" "$runtime" \
  && /tests/common/hardened-run.sh run "$binary" "$runtime/scrape" \
    '^(TestMicro1UnixSocketPoolIdentity|TestMicro1UnixSocketCallerDialOption|TestMicro1UnixSocketClientRetirement|TestMicro1UnixSocketTransportMarkerRetirement|TestZZZZMicro1VerifierProof)$' "$proof"; then
  reward=1
  status=passed
else
  reward=0
  status=behavioral_failure
fi
printf '%s\n' "$reward" > /logs/verifier/reward.txt
printf '{"phase":"verify","status":"%s","reward":%s}\n' "$status" "$reward" > /logs/verifier/status.json
