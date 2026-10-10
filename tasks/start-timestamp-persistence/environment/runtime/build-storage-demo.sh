#!/bin/sh
set -eu
source_root=$1
output=$2
assets=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# Build from an isolated copy of the command package; never edit candidate files.
probe_dir=$(mktemp -d "$source_root/.public-storage-build.XXXXXX")
trap 'rm -rf "$probe_dir"' EXIT
cp "$source_root"/cmd/prometheus/*.go "$probe_dir/"
rm -f "$probe_dir"/*_test.go
sed 's/^func main() {/func publicStorageDemo() {/' "$assets/storage-demo.go" > "$probe_dir/zz_public_storage_demo.go"
cp "$assets/feature-bridge.go" "$probe_dir/zz_public_feature_bridge.go"
cd "$source_root"
GOWORK=off go build -o "$output" "./$(basename "$probe_dir")"
