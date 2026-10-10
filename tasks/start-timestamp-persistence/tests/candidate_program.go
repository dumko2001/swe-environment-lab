package main

import (
	"context"
	"math"
	"os"
	"strconv"

	"github.com/prometheus/common/promslog"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/value"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/tsdbutil"
)

const lifecycleSamples = 130

func options(xor2, stStorage, histogramEncoding bool) *tsdb.Options {
	opts := publicStorageOptions(xor2, histogramEncoding)
	opts.MinBlockDuration = 100
	opts.MaxBlockDuration = 100
	opts.EnableSTStorage = stStorage
	opts.XOR2EncodingAllowed = xor2
	if xor2 {
		opts.FloatChunkEncoding = chunkenc.EncXOR2
	} else {
		opts.FloatChunkEncoding = chunkenc.EncXOR
	}
	opts.OutOfOrderTimeWindow = 1000
	return opts
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
			h.CounterResetHint = histogram.CounterReset
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

// Write ordinary float samples using only public feature parsing. ST storage is
// deliberately off: histogram encoding must not change the float codec.
func writeFloatEncoding(dir, metric string, base, count int64, xor2, histogramEncoding bool) {
	var flags []string
	if xor2 {
		flags = append(flags, "xor2-encoding")
	}
	if histogramEncoding {
		flags = append(flags, "histograms-st-encoding")
	}
	opts := publicFeatureOptions(flags)
	opts.MinBlockDuration, opts.MaxBlockDuration = 100, 100
	db, err := tsdb.Open(dir, promslog.NewNopLogger(), nil, opts, nil)
	if err != nil {
		panic(err)
	}
	db.DisableCompactions()
	app := db.AppenderV2(context.Background())
	for i := int64(0); i < 4; i++ {
		if _, err := app.Append(0, labels.FromStrings("__name__", metric), 0, base+i, float64(count+i), nil, nil, storage.AppendV2Options{}); err != nil {
			panic(err)
		}
	}
	if err := app.Commit(); err != nil {
		panic(err)
	}
	if err := db.CompactHead(tsdb.NewRangeHead(db.Head(), base, base+3)); err != nil {
		panic(err)
	}
	if err := db.Close(); err != nil {
		panic(err)
	}
}

func main() {
	if len(os.Args) != 11 {
		panic("invalid arguments")
	}
	mode, dir, metric := os.Args[1], os.Args[2], os.Args[3]
	st, err := strconv.ParseInt(os.Args[4], 10, 64)
	if err != nil {
		panic(err)
	}
	base, err := strconv.ParseInt(os.Args[5], 10, 64)
	if err != nil {
		panic(err)
	}
	count, err := strconv.ParseInt(os.Args[6], 10, 64)
	if err != nil {
		panic(err)
	}
	floatMode, err := strconv.ParseBool(os.Args[7])
	if err != nil {
		panic(err)
	}
	xor2, err := strconv.ParseBool(os.Args[8])
	if err != nil {
		panic(err)
	}
	profile, err := strconv.Atoi(os.Args[10])
	if err != nil {
		panic(err)
	}

	if mode == "float-encoding-disabled" || mode == "float-encoding-enabled" {
		writeFloatEncoding(dir, metric, base, count, xor2, mode == "float-encoding-enabled")
		return
	}

	histogramEncoding := mode != "legacy" && mode != "disabled"
	opts := options(xor2, true, histogramEncoding)
	db, err := tsdb.Open(dir, promslog.NewNopLogger(), nil, opts, nil)
	if err != nil {
		panic(err)
	}
	db.DisableCompactions()
	appendSample := func(app storage.AppenderV2, t, sampleST int64, n int64, ordinal int) {
		var h *histogram.Histogram
		switch mode {
		case "recode":
			h = recodeHistogram(ordinal)
		case "stale":
			h = staleHistogram(ordinal)
		default:
			h = shapedHistogram(n, profile, ordinal)
		}
		var integer = h
		var floating *histogram.FloatHistogram
		if mode == "stale" && (ordinal == 1 || ordinal == 2) {
			floating = &histogram.FloatHistogram{Sum: math.Float64frombits(value.StaleNaN)}
		} else {
			floating = shapedFloatHistogram(h)
		}
		if err := h.Validate(); err != nil {
			panic(err)
		}
		if err := floating.Validate(); err != nil {
			panic(err)
		}
		if floatMode {
			integer = nil
		} else {
			floating = nil
		}
		if _, err := app.Append(0, labels.FromStrings("__name__", metric), sampleST, t, 0, integer, floating, storage.AppendV2Options{}); err != nil {
			panic(err)
		}
	}

	if mode == "probe" || mode == "reset" || mode == "recode" || mode == "stale" {
		app := db.AppenderV2(context.Background())
		samples := 2
		if mode == "reset" {
			samples = 4
		} else if mode == "recode" {
			samples = 3
		} else if mode == "stale" {
			samples = 4
		}
		for ordinal := 0; ordinal < samples; ordinal++ {
			sampleST := int64(0)
			if ordinal%3 == 0 {
				sampleST = st + int64(ordinal)
			}
			appendSample(app, base+int64(ordinal), sampleST, count+int64(ordinal), ordinal)
		}
		if err := app.Commit(); err != nil {
			panic(err)
		}
		if err := db.Close(); err != nil {
			panic(err)
		}
		db, err = tsdb.Open(dir, promslog.NewNopLogger(), nil, opts, nil)
		if err != nil {
			panic(err)
		}
		db.DisableCompactions()
		if err := db.CompactHead(tsdb.NewRangeHead(db.Head(), base, base+int64(samples-1))); err != nil {
			panic(err)
		}
	} else if mode == "inorder" || mode == "compact" || mode == "legacy" || mode == "disabled" {
		app := db.AppenderV2(context.Background())
		for ordinal := 0; ordinal < lifecycleSamples; ordinal++ {
			sampleST := int64(0)
			if mode == "compact" {
				sampleST = st
			} else if ordinal%3 == 0 {
				sampleST = st + int64(ordinal)
			}
			appendSample(app, base+int64(ordinal), sampleST, count+int64(ordinal), ordinal)
		}
		if err := app.Commit(); err != nil {
			panic(err)
		}
		if err := db.Close(); err != nil {
			panic(err)
		}
		db, err = tsdb.Open(dir, promslog.NewNopLogger(), nil, opts, nil)
		if err != nil {
			panic(err)
		}
		db.DisableCompactions()
		if err := db.CompactHead(tsdb.NewRangeHead(db.Head(), base, base+lifecycleSamples-1)); err != nil {
			panic(err)
		}
	} else if mode == "ooo" {
		app := db.AppenderV2(context.Background())
		appendSample(app, base+200, st+200, count+200, 0)
		if err := app.Commit(); err != nil {
			panic(err)
		}
		app = db.AppenderV2(context.Background())
		for ordinal := 1; ordinal < lifecycleSamples; ordinal++ {
			sampleST := int64(0)
			if ordinal%3 == 0 {
				sampleST = st + int64(ordinal)
			}
			appendSample(app, base+int64(ordinal-1), sampleST, count+int64(ordinal-1), ordinal)
		}
		if err := app.Commit(); err != nil {
			panic(err)
		}
		if err := db.Close(); err != nil {
			panic(err)
		}
		db, err = tsdb.Open(dir, promslog.NewNopLogger(), nil, opts, nil)
		if err != nil {
			panic(err)
		}
		db.DisableCompactions()
		if err := db.CompactOOOHead(context.Background()); err != nil {
			panic(err)
		}
		if err := db.CompactHead(tsdb.NewRangeHead(db.Head(), base+200, base+200)); err != nil {
			panic(err)
		}
	} else {
		panic("invalid mode")
	}
	if err := db.Close(); err != nil {
		panic(err)
	}
}
