#!/bin/sh
set -eu

mkdir -p /logs/verifier
work=/tmp/candidate
candidate_program=/tmp/micro1-st-program
candidate_reader=/tmp/micro1-st-candidate-reader
legacy_writer=/tmp/micro1-st-legacy-writer
oracle_reader=/tmp/micro1-st-oracle-reader
controller=/tmp/micro1-st-controller
prometheus_server=/tmp/micro1-st-prometheus
runtime=/tmp/micro1-runtime

if /tests/common/prepare-candidate.sh /app/verifier-artifacts/candidate.tar.gz /app/verifier-artifacts/candidate.sha256 "$work"; then
  :
else
  code=$?
  test "$code" -ne 20 || exit "$code"
  printf '0\n' > /logs/verifier/reward.txt
  exit 0
fi

if ! (MICRO1_GOMAXPROCS=2 MICRO1_GOFLAGS='-mod=mod -p=1' sh /tests/build-storage-probe.sh "$work/prometheus" /tests/candidate_program.go "$candidate_program" \
		&& MICRO1_GOMAXPROCS=2 MICRO1_GOFLAGS='-mod=mod -p=1' sh /tests/build-storage-probe.sh "$work/prometheus" /tests/candidate_reader.go "$candidate_reader" \
		&& MICRO1_GOMAXPROCS=2 MICRO1_GOFLAGS='-mod=mod -p=1' /tests/common/hardened-run.sh compile-program "$work/prometheus" ./cmd/prometheus "$prometheus_server" \
		&& MICRO1_GOMAXPROCS=2 MICRO1_GOFLAGS='-mod=mod -p=1' /tests/common/hardened-run.sh compile-trusted-controller /opt/reference/source /tests/legacy_writer.go "$legacy_writer" \
		&& MICRO1_GOMAXPROCS=2 MICRO1_GOFLAGS='-mod=mod -p=1' /tests/common/hardened-run.sh compile-trusted-controller /opt/oracle/source /tests/oracle_reader.go "$oracle_reader" \
		&& MICRO1_GOMAXPROCS=2 MICRO1_GOFLAGS='-mod=mod -p=1' /tests/common/hardened-run.sh compile-trusted-controller /opt/reference/source /tests/controller.go "$controller"); then
  printf '0\n' > /logs/verifier/reward.txt
  printf '%s\n' '{"phase":"build","status":"candidate_build_failed","reward":0}' > /logs/verifier/status.json
  exit 0
fi
/tests/common/hardened-run.sh seal "$work/prometheus" "$runtime"
chown -R root:root /opt/oracle
find /opt/oracle -type d -exec chmod 0700 {} +
find /opt/oracle -type f -exec chmod 0600 {} +

if /tests/common/hardened-run.sh run-controller "$controller" /tmp "$candidate_program" "$candidate_reader" "$legacy_writer" "$oracle_reader" "$prometheus_server"; then
  reward=1
  status=passed
else
  reward=0
  status=behavioral_failure
fi
printf '%s\n' "$reward" > /logs/verifier/reward.txt
printf '{"phase":"verify","status":"%s","reward":%s}\n' "$status" "$reward" > /logs/verifier/status.json
