package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/common/promslog"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/value"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/tsdbutil"
)

const lifecycleSamples = 130

type sample struct {
	ST      int64   `json:"st"`
	T       int64   `json:"t"`
	Receipt receipt `json:"receipt"`
}

type readResult struct {
	SeriesCount int      `json:"series_count"`
	Samples     []sample `json:"samples"`
	Seek        *sample  `json:"seek"`
	PastEnd     bool     `json:"past_end"`
	BlockBytes  int64    `json:"-"`
}

type integerBucket struct {
	Index int32  `json:"index"`
	Count uint64 `json:"count"`
}

type floatBucket struct {
	Index     int32  `json:"index"`
	CountBits uint64 `json:"count_bits"`
}

type integerPayload struct {
	Count     uint64          `json:"count"`
	ZeroCount uint64          `json:"zero_count"`
	Positive  []integerBucket `json:"positive"`
	Negative  []integerBucket `json:"negative"`
}

type floatPayload struct {
	CountBits     uint64        `json:"count_bits"`
	ZeroCountBits uint64        `json:"zero_count_bits"`
	Positive      []floatBucket `json:"positive"`
	Negative      []floatBucket `json:"negative"`
}

type receipt struct {
	Kind              string          `json:"kind"`
	ResetHint         string          `json:"reset_hint"`
	Schema            int32           `json:"schema"`
	ZeroThresholdBits uint64          `json:"zero_threshold_bits"`
	SumBits           uint64          `json:"sum_bits"`
	CustomValueBits   []uint64        `json:"custom_value_bits"`
	Integer           *integerPayload `json:"integer,omitempty"`
	Float             *floatPayload   `json:"float,omitempty"`
}

func token() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func stopCandidateProcesses() {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == 1 {
			continue
		}
		status, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(status), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "Uid:" && fields[1] == "65532" {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				break
			}
		}
	}
}

func blockName(name string) bool {
	if len(name) != 26 {
		return false
	}
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for _, character := range name {
		if !strings.ContainsRune(alphabet, character) {
			return false
		}
	}
	return true
}

func copyRegularTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular block entry: %s", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(output, input); err != nil {
			output.Close()
			return err
		}
		return output.Close()
	})
}

func projectBlocks(source, destination string) error {
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	blocks := 0
	for _, entry := range entries {
		if !entry.IsDir() || !blockName(entry.Name()) {
			continue
		}
		blockSource := filepath.Join(source, entry.Name())
		if _, err := os.Stat(filepath.Join(blockSource, "meta.json")); err != nil {
			return err
		}
		if err := copyRegularTree(blockSource, filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
		blocks++
	}
	if blocks == 0 {
		return fmt.Errorf("candidate produced no durable block")
	}
	return nil
}

func blockBytes(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func sealReadOnly(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(path, 0, 0); err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(path, 0555)
		}
		return os.Chmod(path, 0444)
	})
}

func hintName(hint histogram.CounterResetHint) string {
	switch hint {
	case histogram.UnknownCounterReset:
		return "unknown"
	case histogram.CounterReset:
		return "reset"
	case histogram.NotCounterReset:
		return "not-reset"
	case histogram.GaugeType:
		return "gauge"
	default:
		panic("invalid counter-reset hint")
	}
}

func customValueBits(values []float64) []uint64 {
	result := make([]uint64, len(values))
	for i, value := range values {
		result[i] = math.Float64bits(value)
	}
	return result
}

func integerReceipt(h *histogram.Histogram) receipt {
	result := receipt{
		Kind:              "histogram",
		ResetHint:         hintName(h.CounterResetHint),
		Schema:            h.Schema,
		ZeroThresholdBits: math.Float64bits(h.ZeroThreshold),
		SumBits:           math.Float64bits(h.Sum),
		CustomValueBits:   customValueBits(h.CustomValues),
		Integer: &integerPayload{
			Count:     h.Count,
			ZeroCount: h.ZeroCount,
			Positive:  []integerBucket{},
			Negative:  []integerBucket{},
		},
	}
	positive := h.PositiveBucketIterator()
	for positive.Next() {
		bucket := positive.At()
		if bucket.Count != 0 {
			result.Integer.Positive = append(result.Integer.Positive, integerBucket{Index: bucket.Index, Count: bucket.Count})
		}
	}
	negative := h.NegativeBucketIterator()
	for negative.Next() {
		bucket := negative.At()
		if bucket.Count != 0 {
			result.Integer.Negative = append(result.Integer.Negative, integerBucket{Index: bucket.Index, Count: bucket.Count})
		}
	}
	return result
}

func floatReceipt(h *histogram.FloatHistogram) receipt {
	result := receipt{
		Kind:              "float-histogram",
		ResetHint:         hintName(h.CounterResetHint),
		Schema:            h.Schema,
		ZeroThresholdBits: math.Float64bits(h.ZeroThreshold),
		SumBits:           math.Float64bits(h.Sum),
		CustomValueBits:   customValueBits(h.CustomValues),
		Float: &floatPayload{
			CountBits:     math.Float64bits(h.Count),
			ZeroCountBits: math.Float64bits(h.ZeroCount),
			Positive:      []floatBucket{},
			Negative:      []floatBucket{},
		},
	}
	positive := h.PositiveBucketIterator()
	for positive.Next() {
		bucket := positive.At()
		if bucket.Count != 0 {
			result.Float.Positive = append(result.Float.Positive, floatBucket{Index: bucket.Index, CountBits: math.Float64bits(bucket.Count)})
		}
	}
	negative := h.NegativeBucketIterator()
	for negative.Next() {
		bucket := negative.At()
		if bucket.Count != 0 {
			result.Float.Negative = append(result.Float.Negative, floatBucket{Index: bucket.Index, CountBits: math.Float64bits(bucket.Count)})
		}
	}
	return result
}

func lastTimestamp(mode string, base int64) int64 {
	switch mode {
	case "probe":
		return base + 1
	case "reset", "stale":
		return base + 3
	case "recode":
		return base + 2
	case "inorder", "compact", "disabled":
		return base + lifecycleSamples - 1
	case "ooo":
		return base + 200
	default:
		panic("invalid lifecycle mode")
	}
}

func tryRun(candidate, candidateReader, oracleReader, mode string, floatMode, xor2 bool, st, base, count int64, optionIndex, profile int) (readResult, error) {
	defer stopCandidateProcesses()
	root, err := os.MkdirTemp("/tmp/micro1-verifier-tmp", "st-state-")
	if err != nil {
		return readResult{}, err
	}
	defer os.RemoveAll(root)
	if err := os.Chown(root, 65532, 65532); err != nil {
		return readResult{}, err
	}
	dir := root + "/db"
	metric := "metric_" + token()
	launcher := os.Getenv("MICRO1_CANDIDATE_LAUNCHER")
	cmd := exec.Command(launcher, candidate, "/tmp/micro1-verifier-tmp", mode, dir, metric, fmt.Sprint(st), fmt.Sprint(base), fmt.Sprint(count), fmt.Sprint(floatMode), fmt.Sprint(xor2), fmt.Sprint(optionIndex), fmt.Sprint(profile))
	output, runErr := cmd.CombinedOutput()
	stopCandidateProcesses()
	if runErr != nil {
		return readResult{}, fmt.Errorf("candidate failed: %w: %s", runErr, output)
	}
	projection := root + "/blocks-only"
	if err := projectBlocks(dir, projection); err != nil {
		return readResult{}, err
	}
	storageBytes, err := blockBytes(projection)
	if err != nil {
		return readResult{}, err
	}
	if err := sealReadOnly(projection); err != nil {
		return readResult{}, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return readResult{}, err
	}
	sandbox := root + "/block-reader-sandbox"
	if err := os.Mkdir(sandbox, 0700); err != nil {
		return readResult{}, err
	}
	if err := os.Chown(sandbox, 65532, 65532); err != nil {
		return readResult{}, err
	}
	if err := os.Chown(root, 0, 0); err != nil {
		return readResult{}, err
	}
	if err := os.Chmod(root, 0555); err != nil {
		return readResult{}, err
	}
	histogramEncoding := mode != "disabled"
	blockOutput, err := exec.Command(
		launcher, candidateReader, "/tmp/micro1-verifier-tmp",
		projection, metric, fmt.Sprint(base+1), fmt.Sprint(optionIndex), fmt.Sprint(histogramEncoding), sandbox, fmt.Sprint(lastTimestamp(mode, base)),
	).CombinedOutput()
	stopCandidateProcesses()
	if err != nil {
		return readResult{}, fmt.Errorf("candidate block reader failed: %w: %s", err, blockOutput)
	}
	blockResult := readResult{Samples: []sample{}}
	if err := json.Unmarshal(blockOutput, &blockResult); err != nil {
		return readResult{}, err
	}
	if oracleReader != "" {
		oracleOutput, err := exec.Command(
			oracleReader,
			projection, metric, fmt.Sprint(base+1), "-2", fmt.Sprint(histogramEncoding), "-", fmt.Sprint(lastTimestamp(mode, base)),
		).CombinedOutput()
		if err != nil {
			return readResult{}, fmt.Errorf("landed-format reader rejected candidate block: %w: %s", err, oracleOutput)
		}
		oracleResult := readResult{Samples: []sample{}}
		if err := json.Unmarshal(oracleOutput, &oracleResult); err != nil {
			return readResult{}, err
		}
		if !reflect.DeepEqual(blockResult.Samples, oracleResult.Samples) ||
			blockResult.SeriesCount != oracleResult.SeriesCount ||
			!reflect.DeepEqual(blockResult.Seek, oracleResult.Seek) ||
			blockResult.PastEnd != oracleResult.PastEnd {
			return readResult{}, fmt.Errorf("candidate block is not interoperable with the landed persistent histogram format")
		}
	}
	blockResult.BlockBytes = storageBytes
	return blockResult, nil
}

func run(candidate, candidateReader, oracleReader, mode string, floatMode, xor2 bool, st, base, count int64, optionIndex, profile int) readResult {
	result, err := tryRun(candidate, candidateReader, oracleReader, mode, floatMode, xor2, st, base, count, optionIndex, profile)
	if err != nil {
		panic(err)
	}
	return result
}

func runLegacy(candidateReader, legacyWriter string, floatMode bool, st, base, count int64, profile int) readResult {
	defer stopCandidateProcesses()
	root, err := os.MkdirTemp("/tmp/micro1-verifier-tmp", "st-legacy-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	dir := root + "/db"
	metric := "metric_" + token()
	if output, err := exec.Command(
		legacyWriter, dir, metric, fmt.Sprint(st), fmt.Sprint(base), fmt.Sprint(count), fmt.Sprint(floatMode), fmt.Sprint(profile),
	).CombinedOutput(); err != nil {
		panic(fmt.Sprintf("legacy writer failed: %v: %s", err, output))
	}
	projection := root + "/blocks-only"
	if err := projectBlocks(dir, projection); err != nil {
		panic(err)
	}
	if err := sealReadOnly(projection); err != nil {
		panic(err)
	}
	sandbox := root + "/reader-sandbox"
	if err := os.Mkdir(sandbox, 0700); err != nil {
		panic(err)
	}
	if err := os.Chown(sandbox, 65532, 65532); err != nil {
		panic(err)
	}
	if err := os.Chmod(root, 0555); err != nil {
		panic(err)
	}
	launcher := os.Getenv("MICRO1_CANDIDATE_LAUNCHER")
	output, err := exec.Command(
		launcher, candidateReader, "/tmp/micro1-verifier-tmp",
		projection, metric, fmt.Sprint(base+1), "-1", "false", sandbox, fmt.Sprint(base+lifecycleSamples-1),
	).CombinedOutput()
	stopCandidateProcesses()
	if err != nil {
		panic(fmt.Sprintf("candidate legacy reader failed: %v: %s", err, output))
	}
	result := readResult{Samples: []sample{}}
	if err := json.Unmarshal(output, &result); err != nil {
		panic(err)
	}
	return result
}

func requireExact(samples, expected []sample, exactHints bool) {
	if len(samples) != len(expected) {
		panic(fmt.Sprintf("candidate reader recovered %d samples, expected %d", len(samples), len(expected)))
	}
	for index, got := range samples {
		want := expected[index]
		if !exactHints {
			if (got.Receipt.ResetHint == "gauge") != (want.Receipt.ResetHint == "gauge") {
				panic("candidate reader changed histogram counter/gauge semantics")
			}
			got.Receipt.ResetHint = want.Receipt.ResetHint
		}
		if !reflect.DeepEqual(got, want) {
			panic("candidate reader did not recover the complete expected histogram sample")
		}
	}
}

func requireResult(result readResult, expected []sample, exactHints bool, seekT int64) {
	if result.SeriesCount != 1 {
		panic("candidate reader returned an unexpected series count")
	}
	if !result.PastEnd {
		panic("candidate iterator Seek past end did not terminate cleanly")
	}
	requireExact(result.Samples, expected, exactHints)
	var want *sample
	for i := range expected {
		if expected[i].T >= seekT && (want == nil || expected[i].T < want.T) {
			candidate := expected[i]
			want = &candidate
		}
	}
	if want == nil || result.Seek == nil {
		panic("candidate iterator Seek did not return the expected histogram sample")
	}
	got := *result.Seek
	if !exactHints {
		if (got.Receipt.ResetHint == "gauge") != (want.Receipt.ResetHint == "gauge") {
			panic("candidate iterator Seek changed histogram counter/gauge semantics")
		}
		got.Receipt.ResetHint = want.Receipt.ResetHint
	}
	if !reflect.DeepEqual(got, *want) {
		panic("candidate iterator Seek returned the wrong complete histogram sample")
	}
}

func shapedHistogram(n int64, profile, ordinal int) *histogram.Histogram {
	if profile == 0 {
		sequence := [...]int64{0, 1, 0, 1}
		n = n - int64(ordinal) + sequence[ordinal%4]
	}
	var h *histogram.Histogram
	if profile == 3 {
		h = tsdbutil.GenerateTestCustomBucketsHistogram(n)
	} else {
		h = tsdbutil.GenerateTestHistogram(n)
	}
	switch profile {
	case 0:
		h.Schema = -2
		switch ordinal % 4 {
		case 0, 2:
			h.CounterResetHint = histogram.UnknownCounterReset
		case 1, 3:
			h.CounterResetHint = histogram.NotCounterReset
		}
	case 1:
		h.Schema = 3
		h.CounterResetHint = histogram.GaugeType
	case 2:
		h.Schema = 1
		if ordinal == 0 {
			h.CounterResetHint = histogram.UnknownCounterReset
		} else {
			h.CounterResetHint = histogram.NotCounterReset
		}
	case 3:
		h.CounterResetHint = histogram.GaugeType
	default:
		panic("invalid histogram profile")
	}
	return h
}

func shapedFloatHistogram(h *histogram.Histogram) *histogram.FloatHistogram {
	result := h.ToFloat(nil)
	result.Count *= 1.5
	result.ZeroCount *= 1.5
	result.Sum *= 1.5
	for i := range result.PositiveBuckets {
		result.PositiveBuckets[i] *= 1.5
	}
	for i := range result.NegativeBuckets {
		result.NegativeBuckets[i] *= 1.5
	}
	return result
}

func recodeHistogram(ordinal int) *histogram.Histogram {
	h := &histogram.Histogram{
		Count:         27,
		ZeroCount:     2,
		Sum:           18.4,
		ZeroThreshold: 1e-125,
		Schema:        1,
		PositiveSpans: []histogram.Span{
			{Offset: 0, Length: 2},
			{Offset: 2, Length: 1},
			{Offset: 3, Length: 2},
			{Offset: 3, Length: 1},
			{Offset: 1, Length: 1},
		},
		PositiveBuckets: []int64{6, -3, 0, -1, 2, 1, -4},
		NegativeSpans:   []histogram.Span{{Offset: 1, Length: 1}},
		NegativeBuckets: []int64{1},
	}
	if ordinal == 0 {
		return h
	}
	h.PositiveSpans = []histogram.Span{
		{Offset: 0, Length: 3},
		{Offset: 1, Length: 1},
		{Offset: 1, Length: 4},
		{Offset: 3, Length: 3},
	}
	h.NegativeSpans = []histogram.Span{{Offset: 0, Length: 2}}
	h.Count = 36
	h.ZeroCount = 3
	h.Sum = 30
	h.PositiveBuckets = []int64{7, -2, -4, 2, -2, -1, 2, 3, 0, -5, 1}
	h.NegativeBuckets = []int64{2, -1}
	if ordinal == 1 {
		return h
	}
	if ordinal == 2 {
		h.ZeroThreshold = 1e-120
		return h
	}
	panic("invalid recode sample")
}

func staleHistogram(ordinal int) *histogram.Histogram {
	if ordinal == 0 {
		return shapedHistogram(17, 2, 0)
	}
	if ordinal == 1 || ordinal == 2 {
		return &histogram.Histogram{Sum: math.Float64frombits(value.StaleNaN)}
	}
	if ordinal == 3 {
		return shapedHistogram(18, 2, 0)
	}
	panic("invalid stale sample")
}

func expectedSpecialSample(mode string, floatMode bool, st, timestamp int64, ordinal int) sample {
	result := sample{ST: st, T: timestamp}
	var h *histogram.Histogram
	if mode == "stale" {
		h = staleHistogram(ordinal)
	} else {
		h = recodeHistogram(ordinal)
	}
	if floatMode {
		if mode == "stale" && (ordinal == 1 || ordinal == 2) {
			result.Receipt = floatReceipt(&histogram.FloatHistogram{Sum: math.Float64frombits(value.StaleNaN)})
		} else {
			result.Receipt = floatReceipt(shapedFloatHistogram(h))
		}
	} else {
		result.Receipt = integerReceipt(h)
	}
	return result
}

func expectedSpecial(mode string, floatMode bool, st, base int64) []sample {
	samples := 3
	if mode == "stale" {
		samples = 4
	}
	result := make([]sample, 0, samples)
	for ordinal := 0; ordinal < samples; ordinal++ {
		sampleST := int64(0)
		if ordinal%3 == 0 {
			sampleST = st + int64(ordinal)
		}
		result = append(result, expectedSpecialSample(mode, floatMode, sampleST, base+int64(ordinal), ordinal))
	}
	return result
}

func expectedSample(floatMode bool, st, timestamp, n int64, profile, ordinal int) sample {
	result := sample{ST: st, T: timestamp}
	h := shapedHistogram(n, profile, ordinal)
	if floatMode {
		result.Receipt = floatReceipt(shapedFloatHistogram(h))
	} else {
		result.Receipt = integerReceipt(h)
	}
	return result
}

func expectedInOrder(floatMode, legacy bool, st, base, count int64, profile int) []sample {
	result := make([]sample, 0, lifecycleSamples)
	for ordinal := 0; ordinal < lifecycleSamples; ordinal++ {
		sampleST := int64(0)
		if !legacy && ordinal%3 == 0 {
			sampleST = st + int64(ordinal)
		}
		result = append(result, expectedSample(floatMode, sampleST, base+int64(ordinal), count+int64(ordinal), profile, ordinal))
	}
	return result
}

func expectedCompact(floatMode bool, st, base, count int64, profile int) []sample {
	result := expectedInOrder(floatMode, false, st, base, count, profile)
	for index := range result {
		result[index].ST = st
	}
	return result
}

func expectedReset(floatMode bool, st, base, count int64) []sample {
	result := make([]sample, 0, 4)
	for ordinal := 0; ordinal < 4; ordinal++ {
		sampleST := int64(0)
		if ordinal%3 == 0 {
			sampleST = st + int64(ordinal)
		}
		result = append(result, expectedSample(floatMode, sampleST, base+int64(ordinal), count+int64(ordinal), 0, ordinal))
	}
	return result
}

func expectedOOO(floatMode bool, st, base, count int64, profile int) []sample {
	result := make([]sample, 0, lifecycleSamples)
	for ordinal := 1; ordinal < lifecycleSamples; ordinal++ {
		sampleST := int64(0)
		if ordinal%3 == 0 {
			sampleST = st + int64(ordinal)
		}
		result = append(result, expectedSample(floatMode, sampleST, base+int64(ordinal-1), count+int64(ordinal-1), profile, ordinal))
	}
	result = append(result, expectedSample(floatMode, st+200, base+200, count+200, profile, 0))
	return result
}

type apiResponse struct {
	Status string                     `json:"status"`
	Data   map[string]map[string]bool `json:"data"`
}

func freeAddress() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func stopProcess(cmd *exec.Cmd, done <-chan error) {
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}

func runFeatureCase(binary string, enabled []string) map[string]map[string]bool {
	root, err := os.MkdirTemp("/tmp/micro1-verifier-tmp", "st-cli-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chown(root, 65532, 65532); err != nil {
		panic(err)
	}
	config := filepath.Join(root, "prometheus.yml")
	if err := os.WriteFile(config, []byte("global:\n  scrape_interval: 1h\nscrape_configs: []\n"), 0444); err != nil {
		panic(err)
	}
	address := freeAddress()
	args := []string{
		"--config.file=" + config,
		"--storage.tsdb.path=" + filepath.Join(root, "data"),
		"--web.listen-address=" + address,
		"--log.level=error",
	}
	if len(enabled) > 0 {
		args = append(args, "--enable-feature="+strings.Join(enabled, ","))
	}
	var output bytes.Buffer
	cmd := exec.Command(os.Getenv("MICRO1_CANDIDATE_LAUNCHER"), binary, "/tmp/micro1-verifier-tmp")
	cmd.Args = append(cmd.Args, args...)
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer stopProcess(cmd, done)

	url := "http://" + address + "/api/v1/features"
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			panic(fmt.Sprintf("prometheus exited before readiness: %v: %s", err, output.String()))
		default:
		}
		response, err := client.Get(url)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var decoded apiResponse
		if err := json.Unmarshal(body, &decoded); err != nil || decoded.Status != "success" {
			panic("invalid public feature response")
		}
		return decoded.Data
	}
	panic(fmt.Sprintf("prometheus did not become ready: %s", output.String()))
}

func requireFeatureState(features map[string]map[string]bool, histogramST, xor2 bool) {
	tsdbFeatures, ok := features["tsdb"]
	if !ok {
		panic("public feature response omitted TSDB state")
	}
	if tsdbFeatures["histograms_st_encoding"] != histogramST {
		panic("histogram start-timestamp feature state mismatch")
	}
	if tsdbFeatures["xor2_encoding"] != xor2 {
		panic("XOR2 feature state mismatch")
	}
}

// Inspect actual durable float chunks with the trusted parent reader. A feature
// registry response alone cannot establish independence of storage behavior.
func checkFloatEncodingBlocks(path, sandbox, metric string, base, count int64, xor2 bool) {
	if err := os.MkdirAll(sandbox, 0700); err != nil {
		panic(err)
	}
	db, err := tsdb.OpenDBReadOnly(path, sandbox, promslog.NewNopLogger())
	if err != nil {
		panic(err)
	}
	defer db.Close()
	q, err := db.ChunkQuerier(base, base+3)
	if err != nil {
		panic(err)
	}
	defer q.Close()
	set := q.Select(context.Background(), false, nil, labels.MustNewMatcher(labels.MatchEqual, "__name__", metric))
	expectedEncoding := chunkenc.EncXOR
	if xor2 {
		expectedEncoding = chunkenc.EncXOR2
	}
	seriesCount, sampleCount := 0, int64(0)
	for set.Next() {
		seriesCount++
		it := set.At().Iterator(nil)
		for it.Next() {
			chunk := it.At().Chunk
			if chunk.Encoding() != expectedEncoding {
				panic(fmt.Sprintf("float encoding changed with histogram flag: got %s want %s", chunk.Encoding(), expectedEncoding))
			}
			samples := chunk.Iterator(nil)
			for valueType := samples.Next(); valueType != chunkenc.ValNone; valueType = samples.Next() {
				if valueType != chunkenc.ValFloat {
					panic("float encoding probe returned non-float sample")
				}
				t, value := samples.At()
				if t != base+sampleCount || value != float64(count+sampleCount) {
					panic("float encoding probe changed sample contents")
				}
				sampleCount++
			}
			if err := samples.Err(); err != nil {
				panic(err)
			}
		}
		if err := it.Err(); err != nil {
			panic(err)
		}
	}
	if err := set.Err(); err != nil {
		panic(err)
	}
	if seriesCount != 1 || sampleCount != 4 {
		panic("float encoding probe lost samples")
	}
}

func requireFloatEncoding(candidate string, histogramEncoding, xor2 bool, base, count int64) {
	defer stopCandidateProcesses()
	root, err := os.MkdirTemp("/tmp/micro1-verifier-tmp", "float-codec-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chown(root, 65532, 65532); err != nil {
		panic(err)
	}
	dir, metric := root+"/db", "float_"+token()
	mode := "float-encoding-disabled"
	if histogramEncoding {
		mode = "float-encoding-enabled"
	}
	launcher := os.Getenv("MICRO1_CANDIDATE_LAUNCHER")
	output, err := exec.Command(launcher, candidate, "/tmp/micro1-verifier-tmp", mode, dir, metric, "0", fmt.Sprint(base), fmt.Sprint(count), "false", fmt.Sprint(xor2), "0", "0").CombinedOutput()
	stopCandidateProcesses()
	if err != nil {
		panic(fmt.Sprintf("float encoding writer failed: %v: %s", err, output))
	}
	projection := root + "/blocks-only"
	if err := projectBlocks(dir, projection); err != nil {
		panic(err)
	}
	if err := sealReadOnly(projection); err != nil {
		panic(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		panic(err)
	}
	checkFloatEncodingBlocks(projection, root+"/reader", metric, base, count, xor2)
}

func main() {
	if len(os.Args) != 6 {
		panic("invalid arguments")
	}
	st := int64(1000 + int(token()[0]))
	base := int64(10020 + int(token()[1])%20)
	count := int64(140 + int(token()[2])%20)
	optionIndex := 0 // Reserved positional argument; public feature parsing configures storage.
	for _, floatMode := range []bool{false, true} {
		reset := run(os.Args[1], os.Args[2], os.Args[4], "reset", floatMode, true, st, base, count, optionIndex, 0)
		requireResult(reset, expectedReset(floatMode, st, base, count), true, base+1)
		recode := run(os.Args[1], os.Args[2], os.Args[4], "recode", floatMode, true, st, base, count, optionIndex, 0)
		requireResult(recode, expectedSpecial("recode", floatMode, st, base), false, base+1)
		stale := run(os.Args[1], os.Args[2], os.Args[4], "stale", floatMode, true, st, base, count, optionIndex, 0)
		requireResult(stale, expectedSpecial("stale", floatMode, st, base), false, base+1)
		inorder := run(os.Args[1], os.Args[2], os.Args[4], "inorder", floatMode, true, st, base, count, optionIndex, 0)
		requireResult(inorder, expectedInOrder(floatMode, false, st, base, count, 0), false, base+1)
		disabled := run(os.Args[1], os.Args[2], os.Args[4], "disabled", floatMode, true, st, base, count, optionIndex, 2)
		requireResult(disabled, expectedInOrder(floatMode, true, st, base, count, 2), false, base+1)
		compact := run(os.Args[1], os.Args[2], os.Args[4], "compact", floatMode, true, st, base, count, optionIndex, 2)
		requireResult(compact, expectedCompact(floatMode, st, base, count, 2), false, base+1)
		if compact.BlockBytes > disabled.BlockBytes+64 {
			panic(fmt.Sprintf("constant start timestamps added %d bytes to the durable block; compact encoding required", compact.BlockBytes-disabled.BlockBytes))
		}
		custom := run(os.Args[1], os.Args[2], os.Args[4], "inorder", floatMode, true, st, base, count, optionIndex, 3)
		requireResult(custom, expectedInOrder(floatMode, false, st, base, count, 3), false, base+1)
		ooo := run(os.Args[1], os.Args[2], os.Args[4], "ooo", floatMode, true, st, base, count, optionIndex, 1)
		requireResult(ooo, expectedOOO(floatMode, st, base, count, 1), false, base+1)
		legacy := runLegacy(os.Args[2], os.Args[3], floatMode, st, base, count, 2)
		requireResult(legacy, expectedInOrder(floatMode, true, st, base, count, 2), false, base+1)
	}
	requireFeatureState(runFeatureCase(os.Args[5], nil), false, false)
	requireFeatureState(runFeatureCase(os.Args[5], []string{"histograms-st-encoding"}), true, false)
	requireFeatureState(runFeatureCase(os.Args[5], []string{"xor2-encoding"}), false, true)
	requireFeatureState(runFeatureCase(os.Args[5], []string{"histograms-st-encoding", "xor2-encoding"}), true, true)
	for _, histogramEncoding := range []bool{false, true} {
		for _, xor2 := range []bool{false, true} {
			requireFloatEncoding(os.Args[1], histogramEncoding, xor2, base, count)
		}
	}

}
