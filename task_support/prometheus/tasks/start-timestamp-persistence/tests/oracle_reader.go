package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strconv"

	"github.com/prometheus/common/promslog"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

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
			Count: h.Count, ZeroCount: h.ZeroCount,
			Positive: []integerBucket{}, Negative: []integerBucket{},
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
			CountBits: math.Float64bits(h.Count), ZeroCountBits: math.Float64bits(h.ZeroCount),
			Positive: []floatBucket{}, Negative: []floatBucket{},
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

func readSample(it chunkenc.Iterator, valueType chunkenc.ValueType) sample {
	s := sample{ST: it.AtST(), T: it.AtT()}
	switch valueType {
	case chunkenc.ValHistogram:
		_, h := it.AtHistogram(nil)
		s.Receipt = integerReceipt(h)
	case chunkenc.ValFloatHistogram:
		_, h := it.AtFloatHistogram(nil)
		s.Receipt = floatReceipt(h)
	default:
		panic("unexpected sample type")
	}
	return s
}

func main() {
	if len(os.Args) != 8 {
		panic("invalid arguments")
	}
	seekT, err := strconv.ParseInt(os.Args[3], 10, 64)
	if err != nil {
		panic(err)
	}
	optionIndex, err := strconv.Atoi(os.Args[4])
	if err != nil {
		panic(err)
	}
	histogramEncoding, err := strconv.ParseBool(os.Args[5])
	if err != nil {
		panic(err)
	}
	queryMax, err := strconv.ParseInt(os.Args[7], 10, 64)
	if err != nil {
		panic(err)
	}

	opts := tsdb.DefaultOptions()
	opts.EnableSTStorage = true
	opts.XOR2EncodingAllowed = true
	opts.FloatChunkEncoding = chunkenc.EncXOR2
	opts.OutOfOrderTimeWindow = 1000
	if histogramEncoding {
		if optionIndex == -2 {
			field, ok := reflect.TypeOf(*opts).FieldByName("EnableHistogramSTEncoding")
			if !ok {
				panic("reference histogram start-timestamp option is missing")
			}
			optionIndex = field.Index[0]
		}
		if optionIndex < 0 || optionIndex >= reflect.TypeOf(*opts).NumField() {
			panic("invalid option index")
		}
		field := reflect.ValueOf(opts).Elem().Field(optionIndex)
		if field.Kind() != reflect.Bool || !field.CanSet() || field.Bool() {
			panic("option is not an independently disabled boolean")
		}
		field.SetBool(true)
	}
	var q storage.Querier
	var closeDB func() error
	if os.Args[6] == "-" {
		db, err := tsdb.Open(os.Args[1], promslog.NewNopLogger(), nil, opts, nil)
		if err != nil {
			panic(err)
		}
		closeDB = db.Close
		q, err = db.Querier(-1<<62, queryMax)
	} else {
		db, err := tsdb.OpenDBReadOnly(os.Args[1], os.Args[6], promslog.NewNopLogger())
		if err != nil {
			panic(err)
		}
		closeDB = db.Close
		q, err = db.Querier(-1<<62, queryMax)
	}
	if err != nil {
		panic(err)
	}
	defer closeDB()
	defer q.Close()
	set := q.Select(context.Background(), false, nil, labels.MustNewMatcher(labels.MatchEqual, "__name__", os.Args[2]))
	result := readResult{Samples: []sample{}}
	for set.Next() {
		result.SeriesCount++
		series := set.At()
		it := series.Iterator(nil)
		for valueType := it.Next(); valueType != chunkenc.ValNone; valueType = it.Next() {
			result.Samples = append(result.Samples, readSample(it, valueType))
		}
		if err := it.Err(); err != nil {
			panic(err)
		}
		seekIt := series.Iterator(nil)
		if valueType := seekIt.Seek(seekT); valueType != chunkenc.ValNone {
			if result.Seek != nil {
				panic("query returned more than one matching series")
			}
			s := readSample(seekIt, valueType)
			result.Seek = &s
		}
		if err := seekIt.Err(); err != nil {
			panic(err)
		}
		pastEndIt := series.Iterator(nil)
		if valueType := pastEndIt.Seek(1 << 62); valueType != chunkenc.ValNone {
			panic("iterator Seek past end returned a sample")
		}
		if err := pastEndIt.Err(); err != nil {
			panic(err)
		}
		result.PastEnd = true
	}
	if err := set.Err(); err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
