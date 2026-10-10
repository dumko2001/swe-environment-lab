#!/bin/sh
set -eu
mkdir -p /logs/verifier
if python3 /tests/prepare.py; then :; else
  code=$?
  test "$code" -ne 20 || exit "$code"
  printf '0\n' > /logs/verifier/reward.txt
  exit 0
fi
if ! (cd /tmp/vm-candidate/victoriametrics && GOWORK=off go build -trimpath -buildvcs=false -o /tmp/micro1-vm ./app/victoria-metrics) > /logs/verifier/build.log 2>&1; then
  printf '0\n' > /logs/verifier/reward.txt
  printf '%s\n' '{"phase":"build","status":"candidate_build_failed","reward":0}' > /logs/verifier/status.json
  exit 0
fi
chown root:root /tmp/micro1-vm
chmod 0111 /tmp/micro1-vm
rm -rf /tmp/vm-candidate
chmod 0700 /tests /opt/reference /opt/input /opt/go-build-cache /logs /app/verifier-artifacts
python3 /tests/controller.py /tmp/micro1-vm /logs/verifier
