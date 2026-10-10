package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
)

func token() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type storedSample struct {
	T    int64  `json:"t"`
	Bits uint64 `json:"bits"`
}

type readResult struct {
	Samples    map[string][]storedSample `json:"samples"`
	Checkpoint map[string]bool           `json:"checkpoint"`
	Queryable  map[string]bool           `json:"queryable"`
}

func run(candidate, reader, selected, active string, maxt, target int64, values [3]float64) readResult {
	root, err := os.MkdirTemp("/tmp/micro1-verifier-tmp", "wal-state-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chown(root, 65532, 65532); err != nil {
		panic(err)
	}
	launcher := os.Getenv("MICRO1_CANDIDATE_LAUNCHER")
	cmd := exec.Command(launcher, candidate, "/tmp/micro1-verifier-tmp", root, selected, active, fmt.Sprint(maxt), fmt.Sprint(target), fmt.Sprint(values[0]), fmt.Sprint(values[1]), fmt.Sprint(values[2]))
	if output, err := cmd.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("candidate failed: %v: %s", err, output))
	}
	cmd = exec.Command(reader, root, selected, active)
	output, err := cmd.Output()
	if err != nil {
		panic(err)
	}
	result := readResult{}
	if err := json.Unmarshal(output, &result); err != nil {
		panic(err)
	}
	wantSelected := []storedSample{{T: maxt - 100, Bits: math.Float64bits(values[0])}, {T: maxt, Bits: 0x7ff0000000000002}}
	if !reflect.DeepEqual(result.Samples[selected], wantSelected) {
		panic(fmt.Sprintf("compacted samples changed: got %v want %v", result.Samples[selected], wantSelected))
	}
	wantLatest := storedSample{T: maxt + 100, Bits: math.Float64bits(values[2])}
	activeSamples := result.Samples[active]
	// Head truncation may remove the pre-cutoff sample. If retained in a chunk,
	// it must still have its exact original timestamp and value.
	if len(activeSamples) == 2 && activeSamples[0] == (storedSample{T: maxt - 100, Bits: math.Float64bits(values[1])}) {
		activeSamples = activeSamples[1:]
	}
	if !reflect.DeepEqual(activeSamples, []storedSample{wantLatest}) {
		panic(fmt.Sprintf("active samples changed: got %v", result.Samples[active]))
	}
	return result
}

func main() {
	if len(os.Args) != 3 {
		panic("invalid arguments")
	}
	candidate, err := filepath.Abs(os.Args[1])
	if err != nil {
		panic(err)
	}
	reader, err := filepath.Abs(os.Args[2])
	if err != nil {
		panic(err)
	}
	selected := "selected_" + token()
	active := "active_" + token()
	maxt := int64(10000 + int(token()[0]))

	values := [3]float64{float64(13 + int(token()[0])), float64(211 + int(token()[0])), float64(419 + int(token()[0]))}
	atBoundary := run(candidate, reader, selected, active, maxt, maxt, values)
	if !atBoundary.Checkpoint[selected] || !atBoundary.Checkpoint[active] {
		panic("inclusive checkpoint boundary violated")
	}
	if !atBoundary.Queryable[selected] || !atBoundary.Queryable[active] {
		panic("clean-parent replay lost compacted or active data at the inclusive boundary")
	}
	afterBoundary := run(candidate, reader, selected, active, maxt, maxt+1, values)
	if afterBoundary.Checkpoint[selected] || !afterBoundary.Checkpoint[active] {
		panic("checkpoint expiry or active control violated")
	}
	if !afterBoundary.Queryable[selected] || !afterBoundary.Queryable[active] {
		panic("expiry removed compacted user data or the active control")
	}
}
