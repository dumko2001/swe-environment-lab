#!/bin/sh
set -eu
export MICRO1_GOFLAGS="-mod=readonly -p=2 -tags=slicelabels"
mkdir -p /logs/verifier
printf '0\n' > /logs/verifier/reward.txt
work=/tmp/candidate
if /tests/common/prepare-candidate.sh /app/verifier-artifacts/candidate.tar.gz /app/verifier-artifacts/candidate.sha256 "$work"; then :; else
  code=$?; test "$code" -ne 20 || exit 20; exit 0
fi
if ! /tests/common/hardened-run.sh compile-program "$work/thanos" ./cmd/thanos /tmp/micro1-thanos; then
  printf '{"status":"candidate_build_failed"}\n' > /logs/verifier/status.json
  exit 0
fi
/tests/common/hardened-run.sh seal "$work/thanos" /tmp/micro1-runtime
if python3 /tests/controller.py /tmp/micro1-thanos /opt/reference/thanos /tmp/micro1-runtime; then
  printf '1\n' > /logs/verifier/reward.txt
else
  printf '0\n' > /logs/verifier/reward.txt
fi
