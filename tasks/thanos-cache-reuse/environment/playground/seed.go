// Public input generator. Sends real float/native histogram samples into Prometheus.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	end := time.Now().Unix()/300*300 - 3600
	start := end - 7200
	req := prompb.WriteRequest{}
	for _, instance := range []string{"east", "west"} {
		f := prompb.TimeSeries{Labels: []prompb.Label{{Name: "__name__", Value: "playground_requests_total"}, {Name: "instance", Value: instance}, {Name: "job", Value: "public-seed"}}}
		h := prompb.TimeSeries{Labels: []prompb.Label{{Name: "__name__", Value: "playground_latency_seconds"}, {Name: "instance", Value: instance}, {Name: "job", Value: "public-seed"}}}
		for t := start; t <= end; t += 15 {
			n := (t-start)/15 + 1
			f.Samples = append(f.Samples, prompb.Sample{Timestamp: t * 1000, Value: float64(n * 7)})
			h.Histograms = append(h.Histograms, prompb.Histogram{Timestamp: t * 1000, Count: &prompb.Histogram_CountInt{CountInt: uint64(n * 3)}, Sum: float64(n) * 1.5, Schema: 0, PositiveSpans: []prompb.BucketSpan{{Offset: 0, Length: 1}}, PositiveDeltas: []int64{n * 3}})
		}
		req.Timeseries = append(req.Timeseries, f, h)
	}
	data, err := req.Marshal()
	must(err)
	r, err := http.NewRequest("POST", "http://127.0.0.1:9090/api/v1/write", bytes.NewReader(snappy.Encode(nil, data)))
	must(err)
	r.Header.Set("Content-Type", "application/x-protobuf")
	r.Header.Set("Content-Encoding", "snappy")
	r.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	c := http.Client{Timeout: 30 * time.Second}
	res, err := c.Do(r)
	must(err)
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(res.Body)
		panic(fmt.Sprintf("seed: %s: %s", res.Status, b))
	}
	b, err := json.Marshal(map[string]int64{"start": start, "end": end})
	must(err)
	must(os.WriteFile("/var/lib/task-env/seed.json", b, 0644))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
