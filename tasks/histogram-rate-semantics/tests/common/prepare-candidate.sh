#!/bin/sh
set -eu

archive=${1:-/app/verifier-artifacts/candidate.tar.gz}
checksum=${2:-/app/verifier-artifacts/candidate.sha256}
destination=${3:-/tmp/candidate}
status=/logs/verifier/status.json
listing=/tmp/candidate-files.txt
verbose_listing=/tmp/candidate-files-long.txt

mkdir -p /logs/verifier

# A broken trusted image must not become a candidate failure.
for executable in /tests/common/hardened-run.sh /tests/common/run-candidate.sh /usr/local/go/bin/go; do
  if ! test -x "$executable"; then
    printf '%s\n' '{"phase":"prepare","status":"infrastructure_error","reason":"trusted_executable_unavailable"}' > "$status"
    exit 20
  fi
done

if ! test -s "$archive" || ! test -s "$checksum"; then
  printf '%s\n' '{"phase":"artifact","status":"infrastructure_error"}' > "$status"
  exit 20
fi

if ! cd "$(dirname "$archive")" || ! sha256sum -c "$(basename "$checksum")"; then
  printf '%s\n' '{"phase":"artifact","status":"invalid_candidate","reason":"checksum"}' > "$status"
  exit 10
fi

if ! tar -tzf "$archive" > "$listing" || ! tar -tvzf "$archive" > "$verbose_listing"; then
  printf '%s\n' '{"phase":"artifact","status":"invalid_candidate","reason":"unreadable_archive"}' > "$status"
  exit 10
fi

if awk 'BEGIN { bad=0 } /^\// { bad=1 } /(^|\/)\.\.($|\/)/ { bad=1 } END { exit bad ? 0 : 1 }' "$listing"; then
  printf '%s\n' '{"phase":"artifact","status":"invalid_candidate","reason":"unsafe_path"}' > "$status"
  exit 10
fi

if awk 'BEGIN { bad=0 } substr($1,1,1) != "-" && substr($1,1,1) != "d" { bad=1 } END { exit bad ? 0 : 1 }' "$verbose_listing"; then
  printf '%s\n' '{"phase":"artifact","status":"invalid_candidate","reason":"non_regular_entry"}' > "$status"
  exit 10
fi

file_count=$(wc -l < "$listing")
expanded_bytes=$(awk '{ total += $3 } END { print total + 0 }' "$verbose_listing")
if test "$file_count" -gt 25000 || test "$expanded_bytes" -gt 536870912; then
  printf '%s\n' '{"phase":"artifact","status":"invalid_candidate","reason":"archive_limits"}' > "$status"
  exit 10
fi

rm -rf "$destination"
mkdir -p "$destination"
tar -xzf "$archive" -C "$destination"

candidate="$destination/prometheus"
if ! test -f "$candidate/go.mod" || ! test -f "$candidate/go.sum"; then
  printf '%s\n' '{"phase":"source","status":"invalid_candidate","reason":"missing_module"}' > "$status"
  exit 10
fi
# Candidate-authored tests are not trusted reward logic. Restore every upstream
# test file from the pinned clean parent before injecting task-owned cases.
find "$candidate" -type f -name '*_test.go' -delete
find /opt/reference/source -type f -name '*_test.go' | while IFS= read -r trusted; do
  relative=${trusted#/opt/reference/source/}
  target="$candidate/$relative"
  mkdir -p "$(dirname "$target")"
  cp "$trusted" "$target"
done

printf '%s\n' '{"phase":"prepare","status":"ready"}' > "$status"
