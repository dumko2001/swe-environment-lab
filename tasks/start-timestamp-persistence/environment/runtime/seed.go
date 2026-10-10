// Public sample data generator. All storage writes go through candidate TSDB.
package main

import (
	"context"
	"fmt"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/tsdb"
	"os"
	"time"
)

func main() {
	opts := tsdb.DefaultOptions()
	db, err := tsdb.Open(os.Args[1], promslog.NewNopLogger(), nil, opts, nil)
	must(err)
	db.DisableCompactions()
	now := time.Now().UnixMilli()
	for _, base := range []int64{0, now - 1800000} {
		a := db.Appender(context.Background())
		for i := int64(0); i <= 120; i++ {
			t := base + i*15000
			_, err = a.Append(0, labels.FromStrings("__name__", "demo_counter", "instance", "alpha"), t, float64(i))
			must(err)
			_, err = a.Append(0, labels.FromStrings("__name__", "metric"), t, float64(i%9+1))
			must(err)
			_, err = a.Append(0, labels.FromStrings("__name__", "arg"), t, 0.5)
			must(err)
			n := uint64(i + 1)
			if i >= 70 {
				n = uint64(i - 69)
			}
			h := &histogram.Histogram{Schema: 0, ZeroThreshold: 0.001, Count: n, Sum: float64(n), PositiveSpans: []histogram.Span{{Offset: 0, Length: 1}}, PositiveBuckets: []int64{int64(n)}}
			if i >= 90 {
				h.Schema = 1
			}
			_, err = a.AppendHistogram(0, labels.FromStrings("__name__", "demo_histogram"), t, h, nil)
			must(err)
			_, err = a.AppendHistogram(0, labels.FromStrings("__name__", "demo_float_histogram"), t, nil, h.ToFloat(nil))
			must(err)
			if i < 60 {
				_, err = a.Append(0, labels.FromStrings("__name__", "mixed_types"), t, float64(i))
			} else {
				_, err = a.AppendHistogram(0, labels.FromStrings("__name__", "mixed_types"), t, h, nil)
			}
			must(err)
		}
		must(a.Commit())
	}
	must(db.Close())
	fmt.Println("Public float and native-histogram samples written through TSDB")
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
