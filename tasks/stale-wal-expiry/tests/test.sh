#!/bin/sh
set -eu

mkdir -p /logs/verifier
work=/tmp/candidate
binary=/tmp/micro1-wal-tests
candidate_program=/tmp/micro1-wal-program
trusted_reader=/tmp/micro1-wal-reader
controller=/tmp/micro1-wal-controller
runtime=/tmp/micro1-runtime
proof=/tmp/micro1-wal-proof
checkpoint_binary=/tmp/micro1-checkpoint-tests
checkpoint_proof=/tmp/micro1-checkpoint-proof

if /tests/common/prepare-candidate.sh /app/verifier-artifacts/candidate.tar.gz /app/verifier-artifacts/candidate.sha256 "$work"; then
  :
else
  code=$?
  test "$code" -ne 20 || exit "$code"
  printf '0\n' > /logs/verifier/reward.txt
  exit 0
fi

cp /tests/hidden_wal_expiry_test.go "$work/prometheus/tsdb/micro1_hidden_test.go"
rm -rf "$work/prometheus/cmd/micro1-wal-program"
mkdir -p "$work/prometheus/cmd/micro1-wal-program" /opt/reference/source/cmd/micro1-wal-reader
cp /tests/candidate_program.go "$work/prometheus/cmd/micro1-wal-program/main.go"
cp /tests/trusted_reader.go /opt/reference/source/cmd/micro1-wal-reader/main.go
# Assertions and test-only helpers must not resolve through submitted replacements.
for helper_path in promql/promqltest util/testutil util/teststorage; do
  rm -rf "$work/prometheus/$helper_path"
  cp -R "/opt/reference/source/$helper_path" "$work/prometheus/$helper_path"
done
/tests/isolate-test-helpers /usr/local/go/bin/go /opt/reference/source "$work/prometheus"
if ! (/tests/common/hardened-run.sh compile "$work/prometheus" tsdb tsdb "$binary" "$proof" \
  && /tests/common/hardened-run.sh compile "$work/prometheus" tsdb/wlog wlog "$checkpoint_binary" "$checkpoint_proof" \
  && /tests/common/hardened-run.sh compile-program "$work/prometheus" ./cmd/micro1-wal-program "$candidate_program" \
  && /tests/common/hardened-run.sh compile-program /opt/reference/source ./cmd/micro1-wal-reader "$trusted_reader" \
  && /tests/common/hardened-run.sh compile-controller /tests/controller.go "$controller"); then
  printf '0\n' > /logs/verifier/reward.txt
  printf '%s\n' '{"phase":"build","status":"candidate_build_failed","reward":0}' > /logs/verifier/status.json
  exit 0
fi
/tests/common/hardened-run.sh seal "$work/prometheus" "$runtime" tsdb tsdb/wlog

if /tests/common/hardened-run.sh run "$checkpoint_binary" "$runtime/tsdb/wlog" \
    '^(TestCheckpoint|TestZZZZMicro1VerifierProof)$' "$checkpoint_proof" \
  && /tests/common/hardened-run.sh run-controller "$controller" /tmp "$candidate_program" "$trusted_reader" \
  && /tests/common/hardened-run.sh run "$binary" "$runtime/tsdb" \
  '^(Test(Micro1(EvictedSeries|WalExpiry)|Head_KeepSeriesInWALCheckpoint)|TestZZZZMicro1VerifierProof)' "$proof"; then
  reward=1
  status=passed
else
  reward=0
  status=behavioral_failure
fi
printf '%s\n' "$reward" > /logs/verifier/reward.txt
printf '{"phase":"verify","status":"%s","reward":%s}\n' "$status" "$reward" > /logs/verifier/status.json
