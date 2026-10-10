#!/bin/sh
set -eu
module=$1
source=$2
binary=$3
probe="$module/cmd/prometheus/zz_micro1_storage_probe.go"
bridge="$module/cmd/prometheus/zz_micro1_feature_bridge.go"
trap 'rm -f "$probe" "$bridge"' EXIT
sed 's/^func main() {/func micro1StorageProbe() {/' "$source" > "$probe"
cp /tests/feature_bridge.go "$bridge"
/tests/common/hardened-run.sh compile-program "$module" ./cmd/prometheus "$binary"
