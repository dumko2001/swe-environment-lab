package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/prometheus/common/promslog"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/value"
	"github.com/prometheus/prometheus/tsdb"
)

func main() {
	if len(os.Args) != 9 {
		panic("invalid arguments")
	}
	dir := os.Args[1]
	selected := labels.FromStrings("__name__", os.Args[2])
	active := labels.FromStrings("__name__", os.Args[3])
	maxt, err := strconv.ParseInt(os.Args[4], 10, 64)
	if err != nil {
		panic(err)
	}
	target, err := strconv.ParseInt(os.Args[5], 10, 64)
	if err != nil {
		panic(err)
	}

	values := make([]float64, 3)
	for index := range values {
		values[index], err = strconv.ParseFloat(os.Args[6+index], 64)
		if err != nil {
			panic(err)
		}
	}

	opts := tsdb.DefaultOptions()
	opts.MinBlockDuration = 1000
	opts.MaxBlockDuration = 1000
	db, err := tsdb.Open(dir, promslog.NewNopLogger(), nil, opts, nil)
	if err != nil {
		panic(err)
	}
	db.DisableCompactions()

	app := db.Appender(context.Background())
	selectedRef, err := app.Append(0, selected, maxt-100, values[0])
	if err != nil {
		panic(err)
	}
	if _, err = app.Append(selectedRef, selected, maxt, math.Float64frombits(value.StaleNaN)); err != nil {
		panic(err)
	}
	activeRef, err := app.Append(0, active, maxt-100, values[1])
	if err != nil {
		panic(err)
	}
	if err = app.Commit(); err != nil {
		panic(err)
	}
	if err = db.CompactStaleHead(); err != nil {
		panic(err)
	}

	app = db.Appender(context.Background())
	if _, err = app.Append(activeRef, active, maxt+100, values[2]); err != nil {
		panic(err)
	}
	if err = app.Commit(); err != nil {
		panic(err)
	}

	for mint := target - 3; mint <= target; mint++ {
		if err = db.Head().Truncate(mint); err != nil {
			panic(err)
		}
	}
	if err = db.Close(); err != nil {
		panic(err)
	}
	fmt.Println("ok")
}
