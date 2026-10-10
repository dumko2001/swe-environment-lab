package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/prometheus/common/promslog"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/record"
	"github.com/prometheus/prometheus/tsdb/wlog"
)

type storedSample struct {
	T    int64  `json:"t"`
	Bits uint64 `json:"bits"`
}

type result struct {
	Samples    map[string][]storedSample `json:"samples"`
	Checkpoint map[string]bool           `json:"checkpoint"`
	Queryable  map[string]bool           `json:"queryable"`
}

func main() {
	if len(os.Args) != 4 {
		panic("invalid arguments")
	}
	checkpoint, _, err := wlog.LastCheckpoint(filepath.Join(os.Args[1], "wal"))
	if err != nil {
		panic(err)
	}
	segments, err := wlog.NewSegmentsReader(checkpoint)
	if err != nil {
		panic(err)
	}
	defer segments.Close()

	decodedResult := result{Checkpoint: map[string]bool{}, Queryable: map[string]bool{}, Samples: map[string][]storedSample{}}
	decoder := record.NewDecoder(labels.NewSymbolTable(), promslog.NewNopLogger())
	reader := wlog.NewReader(segments)
	for reader.Next() {
		recordBytes := reader.Record()
		if decoder.Type(recordBytes) != record.Series {
			continue
		}
		series, err := decoder.Series(recordBytes, nil)
		if err != nil {
			panic(err)
		}
		for _, item := range series {
			decodedResult.Checkpoint[item.Labels.Get("__name__")] = true
		}
	}
	if err := reader.Err(); err != nil {
		panic(err)
	}
	db, err := tsdb.Open(os.Args[1], promslog.NewNopLogger(), nil, tsdb.DefaultOptions(), nil)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	querier, err := db.Querier(-1<<62, 1<<62)
	if err != nil {
		panic(err)
	}
	defer querier.Close()
	for _, metric := range os.Args[2:] {
		set := querier.Select(context.Background(), false, nil, labels.MustNewMatcher(labels.MatchEqual, "__name__", metric))
		for set.Next() {
			series := set.At()
			if series.Labels().String() != labels.FromStrings("__name__", metric).String() {
				panic("persisted series labels changed")
			}
			iterator := series.Iterator(nil)
			for kind := iterator.Next(); kind != chunkenc.ValNone; kind = iterator.Next() {
				if kind != chunkenc.ValFloat {
					panic(fmt.Sprintf("unexpected stored sample type %v", kind))
				}
				timestamp, value := iterator.At()
				decodedResult.Samples[metric] = append(decodedResult.Samples[metric], storedSample{T: timestamp, Bits: math.Float64bits(value)})
				decodedResult.Queryable[metric] = true
			}
			if err := iterator.Err(); err != nil {
				panic(err)
			}
		}
		if err := set.Err(); err != nil {
			panic(err)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(decodedResult); err != nil {
		panic(err)
	}
}
