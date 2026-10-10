#!/bin/sh
set -eu

mkdir -p /logs/verifier
work=/tmp/candidate
candidate=/tmp/micro1-prometheus
controller=/tmp/micro1-step-controller
evaluation_probe=/tmp/micro1-step-evaluation-probe
runtime=/tmp/micro1-runtime
config=/tmp/micro1-prometheus.yml

if /tests/common/prepare-candidate.sh /app/verifier-artifacts/candidate.tar.gz /app/verifier-artifacts/candidate.sha256 "$work"; then
  :
else
  code=$?
  test "$code" -ne 20 || exit "$code"
  printf '0\n' > /logs/verifier/reward.txt
  exit 0
fi

mkdir -p "$work/prometheus/cmd/micro1-step-evaluation-probe"
cp /tests/evaluation_probe.go "$work/prometheus/cmd/micro1-step-evaluation-probe/main.go"
if ! /tests/common/hardened-run.sh compile-program "$work/prometheus" ./cmd/prometheus "$candidate" \
  || ! /tests/common/hardened-run.sh compile-program "$work/prometheus" ./cmd/micro1-step-evaluation-probe "$evaluation_probe" \
  || ! /tests/common/hardened-run.sh compile-controller /tests/controller.go "$controller"; then
  printf '0\n' > /logs/verifier/reward.txt
  printf '%s\n' '{"phase":"build","status":"candidate_build_failed","reward":0}' > /logs/verifier/status.json
  exit 0
fi
printf '%s\n' 'global: {}' 'scrape_configs: []' > "$config"
chmod 0644 "$config"
/tests/common/hardened-run.sh seal "$work/prometheus" "$runtime"

challenge=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
receipt=/logs/verifier/evaluation-probe.log
if /tests/common/run-candidate.sh "$evaluation_probe" "$runtime" "$challenge" > "$receipt" \
  && grep -Fxq "evaluation-complete:$challenge" "$receipt" \
  && /tests/common/hardened-run.sh run-controller "$controller" "$runtime" \
    "$candidate" /opt/reference/promtool "$runtime" "$config"; then
  reward=1
  status=passed
else
  reward=0
  status=behavioral_failure
fi
printf '%s\n' "$reward" > /logs/verifier/reward.txt
printf '{"phase":"verify","status":"%s","reward":%s}\n' "$status" "$reward" > /logs/verifier/status.json
