#!/bin/sh
set -eu

binary=$1
cwd=$2
shift 2

case "$binary" in
  /tmp/micro1-*) ;;
  *) printf 'candidate binary is outside the execution allowlist\n' >&2; exit 125 ;;
esac
canonical=$(readlink -f -- "$binary")
test "$canonical" = "$binary" || exit 125
test ! -L "$binary" || exit 125
test -f "$binary" || exit 125
test "$(stat -c %u "$binary")" = 0 || exit 125
test -d "$cwd" || exit 125

cd "$cwd"
candidate_timeout=${MICRO1_CANDIDATE_TIMEOUT_SEC:-300}
case "$candidate_timeout" in
  *[!0-9]*|'') exit 125 ;;
esac
test "$candidate_timeout" -ge 1 && test "$candidate_timeout" -le 900 || exit 125

exec timeout --signal=TERM --kill-after=5s "${candidate_timeout}s" \
  prlimit --nproc=128:128 --nofile=256:256 --core=0:0 -- \
  setpriv \
    --reuid=65532 --regid=65532 --clear-groups \
    --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all \
  env -i \
    HOME=/tmp/micro1-verifier-home \
    TMPDIR=/tmp/micro1-verifier-tmp \
    PATH=/usr/local/go/bin:/usr/bin:/bin \
    "$binary" "$@"
