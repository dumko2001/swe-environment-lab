#!/bin/sh
set -eu

mkdir -p /logs/verifier
work=/tmp/candidate
binary=/tmp/micro1-histogram-tests
candidate=/tmp/micro1-prometheus
controller=/tmp/micro1-histogram-controller
runtime=/tmp/micro1-runtime
proof=/tmp/micro1-histogram-proof
config=/tmp/micro1-prometheus.yml

if /tests/common/prepare-candidate.sh /app/verifier-artifacts/candidate.tar.gz /app/verifier-artifacts/candidate.sha256 "$work"; then
  :
else
  code=$?
  test "$code" -ne 20 || exit "$code"
  printf '0\n' > /logs/verifier/reward.txt
  exit 0
fi

rm -rf "$work/prometheus/promql/promqltest"
cp -R /opt/reference/source/promql/promqltest "$work/prometheus/promql/promqltest"
cp /tests/hidden_histogram_rate_test.go "$work/prometheus/promql/micro1_hidden_test.go"
# Assertions and test-only helpers must not resolve through submitted replacements.
for helper_path in promql/promqltest util/testutil util/teststorage; do
  rm -rf "$work/prometheus/$helper_path"
  cp -R "/opt/reference/source/$helper_path" "$work/prometheus/$helper_path"
done
/tests/isolate-test-helpers /usr/local/go/bin/go /opt/reference/source "$work/prometheus"
if ! /tests/common/hardened-run.sh compile "$work/prometheus" promql promql_test "$binary" "$proof" \
  || ! /tests/common/hardened-run.sh compile-program "$work/prometheus" ./cmd/prometheus "$candidate" \
  || ! /tests/common/hardened-run.sh compile-trusted-controller /opt/reference/source /tests/controller.go "$controller"; then
  printf '0\n' > /logs/verifier/reward.txt
  printf '%s\n' '{"phase":"build","status":"candidate_build_failed","reward":0}' > /logs/verifier/status.json
  exit 0
fi
printf '%s\n' 'global: {}' 'scrape_configs: []' > "$config"
chmod 0644 "$config"
/tests/common/hardened-run.sh seal "$work/prometheus" "$runtime" promql

if /tests/common/hardened-run.sh run-controller "$controller" "$runtime" "$candidate" "$runtime" "$config" \
  && /tests/common/hardened-run.sh run "$binary" "$runtime/promql" \
    '^(TestMicro1NativeHistogramExtendedRates|TestZZZZMicro1VerifierProof)$' "$proof"; then
  reward=1
  status=passed
else
  reward=0
  status=behavioral_failure
fi
printf '%s\n' "$reward" > /logs/verifier/reward.txt
printf '{"phase":"verify","status":"%s","reward":%s}\n' "$status" "$reward" > /logs/verifier/status.json
