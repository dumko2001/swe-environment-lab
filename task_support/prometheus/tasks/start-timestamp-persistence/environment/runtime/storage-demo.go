// Public storage playground. This reports observations; it is not a grader.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func options() *tsdb.Options {
	features, provided := os.LookupEnv("STORAGE_FEATURES")
	if !provided {
		features = "xor2-encoding"
	}
	o := publicStorageOptions(strings.Split(features, ","))
	return o
}
func open(dir string) *tsdb.DB {
	db, err := tsdb.Open(dir, slog.New(slog.NewTextHandler(os.Stderr, nil)), nil, options(), nil)
	must(err)
	db.DisableCompactions()
	return db
}
func report(db *tsdb.DB, phase string) {
	fmt.Println("phase:", phase)
	q, err := db.Querier(0, 300000)
	must(err)
	defer q.Close()
	series := q.Select(context.Background(), true, nil, labels.MustNewMatcher(labels.MatchRegexp, "__name__", "playground_.*"))
	for series.Next() {
		s := series.At()
		it := s.Iterator(nil)
		for kind := it.Next(); kind != chunkenc.ValNone; kind = it.Next() {
			switch kind {
			case chunkenc.ValHistogram:
				t, h := it.AtHistogram(nil)
				fmt.Printf("%s t=%d start=%d count=%d sum=%g\n", s.Labels(), t, it.AtST(), h.Count, h.Sum)
			case chunkenc.ValFloatHistogram:
				t, h := it.AtFloatHistogram(nil)
				fmt.Printf("%s t=%d start=%d count=%g sum=%g\n", s.Labels(), t, it.AtST(), h.Count, h.Sum)
			}
		}
		must(it.Err())
	}
	must(series.Err())
}
func main() {
	if len(os.Args) != 2 {
		panic("usage: storage-demo NEW_DIRECTORY")
	}
	dir := os.Args[1]
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		panic("choose a new directory; existing data is never overwritten")
	}
	must(os.MkdirAll(dir, 0755))
	db := open(dir)
	app := db.AppenderV2(context.Background())
	for i := int64(1); i <= 3; i++ {
		h := &histogram.Histogram{Schema: 0, ZeroThreshold: 0.001, Count: uint64(i), Sum: float64(i), PositiveSpans: []histogram.Span{{Offset: 1, Length: 1}}, PositiveBuckets: []int64{i}}
		_, err := app.Append(0, labels.FromStrings("__name__", "playground_integer"), 60000, 120000+i*1000, 0, h, nil, storage.AppendV2Options{})
		must(err)
		_, err = app.Append(0, labels.FromStrings("__name__", "playground_float"), 60000, 120000+i*1000, 0, nil, h.ToFloat(nil), storage.AppendV2Options{})
		must(err)
	}
	must(app.Commit())
	report(db, "head; submitted start timestamp = 60000")
	must(db.Close())
	db = open(dir)
	report(db, "after WAL reopen")
	must(db.CompactHead(tsdb.NewRangeHead(db.Head(), db.Head().MinTime(), db.Head().MaxTime())))
	must(db.Close())
	db = open(dir)
	report(db, "after block compaction and reopen")
	must(db.Close())
	fmt.Println("Durable TSDB retained at", dir)
}
