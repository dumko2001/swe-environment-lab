// Public storage playground. Real TSDB lifecycle, no pass/fail grading.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/value"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/record"
	"github.com/prometheus/prometheus/tsdb/wlog"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func open(dir string) *tsdb.DB {
	opts := tsdb.DefaultOptions()
	opts.MinBlockDuration = 60000
	opts.MaxBlockDuration = 60000
	db, err := tsdb.Open(dir, slog.New(slog.NewTextHandler(os.Stderr, nil)), nil, opts, nil)
	must(err)
	db.DisableCompactions()
	return db
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
	old := labels.FromStrings("__name__", "playground_retired")
	current := labels.FromStrings("__name__", "playground_current")
	app := db.Appender(context.Background())
	ref, err := app.Append(0, old, 61000, 12)
	must(err)
	_, err = app.Append(ref, old, 90000, math.Float64frombits(value.StaleNaN))
	must(err)
	liveRef, err := app.Append(0, current, 61000, 21)
	must(err)
	must(app.Commit())
	fmt.Println("Compacting retired series into a real block")
	must(db.CompactStaleHead())
	app = db.Appender(context.Background())
	_, err = app.Append(liveRef, current, 121000, 22)
	must(err)
	must(app.Commit())
	fmt.Println("Advancing head truncation through the retired-series timestamp")
	for _, cutoff := range []int64{89997, 89998, 89999, 90000} {
		fmt.Println("truncate", cutoff)
		must(db.Head().Truncate(cutoff))
	}
	must(db.Close())
	fmt.Println("Reopening actual WAL and blocks; inspect replay logs on stderr")
	db = open(dir)
	q, err := db.Querier(0, 180000)
	must(err)
	series := q.Select(context.Background(), true, nil, labels.MustNewMatcher(labels.MatchRegexp, "__name__", "playground_.*"))
	for series.Next() {
		s := series.At()
		it := s.Iterator(nil)
		for kind := it.Next(); kind != chunkenc.ValNone; kind = it.Next() {
			if kind == chunkenc.ValFloat {
				t, v := it.At()
				fmt.Printf("%s t=%d value=%g\n", s.Labels(), t, v)
			}
		}
		must(it.Err())
	}
	must(series.Err())
	must(q.Close())
	must(db.Close())
	entries, err := os.ReadDir(dir + "/wal")
	must(err)
	for _, entry := range entries {
		fmt.Println("wal artifact:", entry.Name())
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "checkpoint.") {
			r, err := wlog.NewSegmentsReader(filepath.Join(dir, "wal", entry.Name()))
			must(err)
			reader := wlog.NewReader(r)
			decoder := record.NewDecoder(nil, slog.Default())
			for reader.Next() {
				if decoder.Type(reader.Record()) == record.Series {
					series, err := decoder.Series(reader.Record(), nil)
					must(err)
					for _, s := range series {
						fmt.Printf("checkpoint series: ref=%d labels=%s\n", s.Ref, s.Labels)
					}
				}
			}
			must(reader.Err())
			must(r.Close())
		}
	}
	fmt.Println("Durable TSDB retained at", dir)
}
