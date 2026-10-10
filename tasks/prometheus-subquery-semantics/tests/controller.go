package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"time"
)

const candidateUID = 65532

type apiResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
}

func randomUint64() uint64 {
	var value uint64
	if err := binary.Read(rand.Reader, binary.LittleEndian, &value); err != nil {
		panic(err)
	}
	return value
}

func reservePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return port, listener.Close()
}

func chownTree(root string) error {
	return filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, candidateUID, candidateUID)
	})
}

func waitReady(endpoint string, deadline time.Time) error {
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("Prometheus did not become ready")
}

func api(base, path string, parameters url.Values) (json.RawMessage, error) {
	response, err := http.Get(base + path + "?" + parameters.Encode())
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var decoded apiResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("invalid API response: %w", err)
	}
	if response.StatusCode != http.StatusOK || decoded.Status != "success" {
		return nil, fmt.Errorf("query failed (%s): %s", decoded.ErrorType, decoded.Error)
	}
	var canonical any
	if err := json.Unmarshal(decoded.Data, &canonical); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(canonical)
	return normalized, err
}

func instant(base, expression, at string) (json.RawMessage, error) {
	return api(base, "/api/v1/query", url.Values{"query": {expression}, "time": {at}})
}

func queryRange(base, expression, start, end, step string) (json.RawMessage, error) {
	return api(base, "/api/v1/query_range", url.Values{
		"query": {expression}, "start": {start}, "end": {end}, "step": {step},
	})
}

func requireEqual(name string, left, right json.RawMessage) error {
	var a, b any
	if err := json.Unmarshal(left, &a); err != nil {
		return err
	}
	if err := json.Unmarshal(right, &b); err != nil {
		return err
	}
	if !equalQueryData(a, b) {
		return fmt.Errorf("%s mismatch\nleft: %s\nright: %s", name, left, right)
	}
	return nil
}

// equalQueryData compares query sample values with the same float64 tolerance
// as independently computed expectations. Timestamps, label strings, result
// shapes and all other response fields remain exact.
func equalQueryData(a, b any) bool {
	switch left := a.(type) {
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, exists := right[key]
			if !exists || !equalQueryData(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		// Prometheus encodes a float sample as [timestamp, "value"].
		if len(left) == 2 {
			lt, leftTimestamp := left[0].(float64)
			rt, rightTimestamp := right[0].(float64)
			lv, leftValue := left[1].(string)
			rv, rightValue := right[1].(string)
			if leftTimestamp && rightTimestamp && leftValue && rightValue {
				if lt != rt {
					return false
				}
				x, xerr := strconv.ParseFloat(lv, 64)
				y, yerr := strconv.ParseFloat(rv, 64)
				if xerr != nil || yerr != nil {
					return false
				}
				if math.IsNaN(x) || math.IsNaN(y) {
					return math.IsNaN(x) && math.IsNaN(y)
				}
				if math.IsInf(x, 0) || math.IsInf(y, 0) {
					return x == y
				}
				return math.Abs(x-y) <= 1e-10*math.Max(1, math.Max(math.Abs(x), math.Abs(y)))
			}
		}
		for index, value := range left {
			if !equalQueryData(value, right[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

func requireNonEmpty(name string, data json.RawMessage) error {
	var decoded struct {
		Result []any `json:"result"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if len(decoded.Result) == 0 {
		return fmt.Errorf("%s returned no series", name)
	}
	return nil
}

func requireMatrix(name string, data json.RawMessage, metricName string, baseTime int64, values []float64) error {
	var decoded struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string   `json:"metric"`
			Values [][]json.RawMessage `json:"values"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.ResultType != "matrix" || len(decoded.Result) != 1 {
		return fmt.Errorf("%s returned the wrong matrix shape: %s", name, data)
	}
	series := decoded.Result[0]
	wantLabels := map[string]string{}
	if metricName != "" {
		wantLabels["__name__"] = metricName
	}
	if !reflect.DeepEqual(series.Metric, wantLabels) {
		return fmt.Errorf("%s returned the wrong metric identity: %s", name, data)
	}
	if len(series.Values) != len(values) {
		return fmt.Errorf("%s returned %d points, expected %d", name, len(series.Values), len(values))
	}
	for index, pair := range series.Values {
		if len(pair) != 2 {
			return fmt.Errorf("%s point %d has the wrong shape", name, index)
		}
		var timestamp float64
		var encodedValue string
		if err := json.Unmarshal(pair[0], &timestamp); err != nil {
			return err
		}
		if err := json.Unmarshal(pair[1], &encodedValue); err != nil {
			return err
		}
		value, err := strconv.ParseFloat(encodedValue, 64)
		if err != nil {
			return err
		}
		if timestamp != float64(baseTime+int64(index*30)) || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value-values[index]) > 1e-10*math.Max(1, math.Abs(values[index])) {
			return fmt.Errorf("%s point %d mismatch: got=%s expected_timestamp=%d expected_value=%.17g", name, index, pair, baseTime+int64(index*30), values[index])
		}
	}
	return nil
}

func requireDifferent(name string, left, right json.RawMessage) error {
	var a, b any
	if err := json.Unmarshal(left, &a); err != nil {
		return err
	}
	if err := json.Unmarshal(right, &b); err != nil {
		return err
	}
	if reflect.DeepEqual(a, b) {
		return fmt.Errorf("%s unexpectedly returned the same result", name)
	}
	return nil
}

func main() {
	if len(os.Args) != 5 {
		panic("usage: controller PROMETHEUS PROMTOOL RUNTIME CONFIG")
	}
	candidate, promtool, runtime, config := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	launcher := os.Getenv("MICRO1_CANDIDATE_LAUNCHER")
	if launcher == "" {
		panic("candidate launcher is unavailable")
	}
	if err := os.MkdirAll(runtime, 0o755); err != nil {
		panic(err)
	}

	baseTime := time.Now().Add(-24 * time.Hour).Unix()
	baseTime -= baseTime % 30
	metricBase := int(randomUint64()%5000) + 100
	argOffset := int(randomUint64() % 8)
	argMetric := fmt.Sprintf("arg_%016x", randomUint64())
	valueMetric := fmt.Sprintf("metric_%016x", randomUint64())
	fixturePath := filepath.Join(runtime, "fixture.openmetrics")
	fixture, err := os.Create(fixturePath)
	if err != nil {
		panic(err)
	}
	_, _ = fmt.Fprintf(fixture, "# TYPE %s gauge\n", argMetric)
	for i := 0; i < 5; i++ {
		fraction := float64(((i+argOffset)%9)+1) / 10
		_, _ = fmt.Fprintf(fixture, "%s %.1f %d\n", argMetric, fraction, baseTime+int64(i*30))
	}
	_, _ = fmt.Fprintf(fixture, "# TYPE %s gauge\n", valueMetric)
	for i := 0; i < 5; i++ {
		_, _ = fmt.Fprintf(fixture, "%s %d %d\n", valueMetric, metricBase+i*(i+3), baseTime+int64(i*30))
	}
	_, _ = fmt.Fprintln(fixture, "# EOF")
	if err := fixture.Close(); err != nil {
		panic(err)
	}
	dataPath := filepath.Join(runtime, "data")
	backfill := exec.Command(promtool, "tsdb", "create-blocks-from", "openmetrics", fixturePath, dataPath)
	backfill.Stdout, backfill.Stderr = os.Stderr, os.Stderr
	if err := backfill.Run(); err != nil {
		panic(fmt.Sprintf("trusted fixture backfill failed: %v", err))
	}
	if err := os.Remove(fixturePath); err != nil {
		panic(err)
	}
	if err := chownTree(runtime); err != nil {
		panic(err)
	}

	port, err := reservePort()
	if err != nil {
		panic(err)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	command := exec.Command(launcher, candidate, runtime,
		"--config.file="+config,
		"--storage.tsdb.path="+dataPath,
		"--storage.tsdb.retention.time=30d",
		"--web.listen-address=127.0.0.1:"+strconv.Itoa(port),
		"--log.level=error")
	command.Stdout, command.Stderr = os.Stderr, os.Stderr
	if err := command.Start(); err != nil {
		panic(err)
	}
	defer func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = command.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = command.Process.Kill()
		}
	}()
	if err := waitReady(baseURL+"/-/ready", time.Now().Add(20*time.Second)); err != nil {
		panic(err)
	}

	start := strconv.FormatInt(baseTime, 10)
	end := strconv.FormatInt(baseTime+90, 10)
	fixed := strconv.FormatInt(baseTime+120, 10)
	rawValues := []float64{
		float64(metricBase),
		float64(metricBase + 4),
		float64(metricBase + 10),
		float64(metricBase + 18),
	}
	rawControl, err := queryRange(baseURL, valueMetric, start, end, "30")
	if err != nil {
		panic(fmt.Sprintf("raw metric control: %v", err))
	}
	if err := requireMatrix("raw metric control", rawControl, valueMetric, baseTime, rawValues); err != nil {
		panic(err)
	}
	doubledControl, err := queryRange(baseURL, "("+valueMetric+") * 2", start, end, "30")
	if err != nil {
		panic(fmt.Sprintf("arithmetic control: %v", err))
	}
	doubledValues := make([]float64, len(rawValues))
	for index, value := range rawValues {
		doubledValues[index] = value * 2
	}
	if err := requireMatrix("arithmetic control", doubledControl, "", baseTime, doubledValues); err != nil {
		panic(err)
	}
	if err := requireDifferent("raw and arithmetic controls", rawControl, doubledControl); err != nil {
		panic(err)
	}
	pairs := []struct {
		name, direct, subquery string
	}{
		{"fixed numeric", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s] @ " + fixed + ")", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s:30s] @ " + fixed + ")"},
		{"fixed with offset", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s] offset 30s @ " + fixed + ")", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s:30s] offset 30s @ " + fixed + ")"},
		{"fixed at start", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s] @ start())", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s:30s] @ start())"},
		{"fixed at end", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s] @ end())", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s:30s] @ end())"},
		{"ordinary", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s])", "quantile_over_time(scalar(" + argMetric + "), " + valueMetric + "[121s:30s])"},
	}
	for _, pair := range pairs {
		directRange, err := queryRange(baseURL, pair.direct, start, end, "30")
		if err != nil {
			panic(fmt.Sprintf("%s direct range: %v", pair.name, err))
		}
		subqueryRange, err := queryRange(baseURL, pair.subquery, start, end, "30")
		if err != nil {
			panic(fmt.Sprintf("%s subquery range: %v", pair.name, err))
		}
		if err := requireEqual(pair.name+" range", directRange, subqueryRange); err != nil {
			panic(err)
		}
		if err := requireNonEmpty(pair.name+" range", subqueryRange); err != nil {
			panic(err)
		}
		directInstant, err := instant(baseURL, pair.direct, start)
		if err != nil {
			panic(fmt.Sprintf("%s direct instant: %v", pair.name, err))
		}
		subqueryInstant, err := instant(baseURL, pair.subquery, start)
		if err != nil {
			panic(fmt.Sprintf("%s subquery instant: %v", pair.name, err))
		}
		if err := requireEqual(pair.name+" instant", directInstant, subqueryInstant); err != nil {
			panic(err)
		}
	}
	nested := "quantile_over_time(scalar(" + argMetric + "), (sum(" + valueMetric + "))[121s:30s] @ " + fixed + ")"
	nestedData, err := queryRange(baseURL, nested, start, end, "30")
	if err != nil {
		panic(fmt.Sprintf("nested fixed subquery: %v", err))
	}
	referenceData, err := queryRange(baseURL, pairs[0].direct, start, end, "30")
	if err != nil {
		panic(err)
	}
	// Compute the quantiles independently: comparing two candidate query paths
	// alone would accept a regression shared by both paths.
	population := []float64{float64(metricBase), float64(metricBase + 4), float64(metricBase + 10), float64(metricBase + 18), float64(metricBase + 28)}
	// Parentheses preserve a standalone fixed subquery's matrix semantics.
	// Check values independently as well as comparing the equivalent forms.
	for _, wrap := range []string{"%s", "(%s)", "((%s))"} {
		expression := fmt.Sprintf(wrap, valueMetric+"[121s:30s] @ "+fixed)
		data, err := instant(baseURL, expression, start)
		if err != nil {
			panic(fmt.Sprintf("standalone fixed subquery %s: %v", expression, err))
		}
		if err := requireMatrix("standalone fixed subquery", data, valueMetric, baseTime, population); err != nil {
			panic(err)
		}
	}
	quantiles := make([]float64, 4)
	for i := range quantiles {
		q := float64(((i+argOffset)%9)+1) / 10
		rank := q * float64(len(population)-1)
		lower := int(math.Floor(rank))
		weight := rank - float64(lower)
		quantiles[i] = population[lower]*(1-weight) + population[lower+1]*weight
	}
	if err := requireMatrix("independent fixed quantiles", referenceData, "", baseTime, quantiles); err != nil {
		panic(err)
	}
	invariantValue := metricBase*3 + 7
	invariantChild := "quantile_over_time(scalar(" + argMetric + "), vector(" + strconv.Itoa(invariantValue) + ")[121s:30s] @ " + fixed + ")"
	invariantData, err := queryRange(baseURL, invariantChild, start, end, "30")
	if err != nil {
		panic(fmt.Sprintf("fixed expression within fixed subquery: %v", err))
	}
	constantValues := []float64{float64(invariantValue), float64(invariantValue), float64(invariantValue), float64(invariantValue)}
	if err := requireMatrix("fixed expression within fixed subquery", invariantData, "", baseTime, constantValues); err != nil {
		panic(err)
	}
	if err := requireEqual("nested fixed subquery", referenceData, nestedData); err != nil {
		panic(err)
	}
	if err := requireDifferent("task query and raw control", referenceData, rawControl); err != nil {
		panic(err)
	}
}
