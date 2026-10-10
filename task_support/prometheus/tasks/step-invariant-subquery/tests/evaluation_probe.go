// Exercise evaluation behavior without prescribing an AST or optimizer design.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/util/annotations"
)

func main() {
	if len(os.Args) != 2 {
		panic("missing completion challenge")
	}
	counts := map[string]int{}
	for _, name := range []string{"pi", "abs"} {
		original := promql.FunctionCalls[name]
		promql.FunctionCalls[name] = func(v []promql.Vector, m promql.Matrix, args parser.Expressions, helper *promql.EvalNodeHelper) (promql.Vector, annotations.Annotations) {
			counts[name]++
			return original(v, m, args, helper)
		}
	}
	engine := promql.NewEngine(promql.EngineOpts{
		MaxSamples: 100000, Timeout: 10 * time.Second, EnableAtModifier: true,
		EnableNegativeOffset: true, NoStepSubqueryIntervalFn: func(int64) int64 { return 30000 },
	})
	defer engine.Close()
	// These expressions need no stored data. The real engine still parses,
	// preprocesses, schedules and evaluates every function and subquery.
	queryable := &storage.MockQueryable{MockQuerier: &storage.MockQuerier{}}
	run := func(expr string, expected func(float64) float64) {
		for key := range counts {
			counts[key] = 0
		}
		query, err := engine.NewRangeQuery(context.Background(), queryable, nil, expr, time.Unix(30, 0), time.Unix(123, 0), 31*time.Second)
		if err != nil {
			panic(err)
		}
		defer query.Close()
		result := query.Exec(context.Background())
		if result.Err != nil {
			panic(result.Err)
		}
		matrix, ok := result.Value.(promql.Matrix)
		if !ok || len(matrix) != 1 || len(matrix[0].Floats) != 4 || matrix[0].Metric.Len() != 0 {
			panic(fmt.Sprintf("wrong evaluation results for %s: %v", expr, result.Value))
		}
		for i, point := range matrix[0].Floats {
			t := float64(30 + i*31)
			want := expected(t)
			if point.T != int64(t)*1000 || math.IsNaN(point.F) || math.IsInf(point.F, 0) || math.Abs(point.F-want) > 1e-12*math.Max(1, math.Abs(want)) {
				panic(fmt.Sprintf("%s: step %d got %+v want %.17g", expr, i, point, want))
			}
		}
		fmt.Printf("evaluated %s; function calls: %v\n", expr, counts)
	}
	// An invariant inner computation must not run at every inner/outer step.
	// Zero calls also permits a valid constant-folding implementation.
	run(`max_over_time(abs(vector(pi()))[121s:30s] @ 120) + time()`, func(t float64) float64 { return math.Pi + t })
	for name, count := range counts {
		if count > 1 {
			panic(fmt.Sprintf("invariant %s repeatedly evaluated %d times", name, count))
		}
	}
	// A varying scalar parameter must keep its outer evaluation time, while the
	// fixed inner population is [0,30,60,90,120]. Linear quantiles equal 120*q.
	run(`quantile_over_time((time() % 100) / 100, vector(time())[121s:30s] @ 120)`, func(t float64) float64 { return 120 * math.Mod(t, 100) / 100 })
	// Ordinary subqueries track their outer time; the latest 30-second grid point
	// in their left-open/right-closed range is the maximum.
	run(`max_over_time(vector(time())[121s:30s])`, func(t float64) float64 { return math.Floor(t/30) * 30 })
	// Top-level matrix results retain their actual inner timestamps. Neither an
	// outer wrapper nor a direct subquery node is required by this assertion.
	for _, suffix := range []string{"@ 120", "@ start()", "@ end()"} {
		for _, wrap := range []string{"%s", "(%s)", "((%s))"} {
			expression := fmt.Sprintf(wrap, "vector(time())[121s:30s] "+suffix)
			query, err := engine.NewInstantQuery(context.Background(), queryable, nil, expression, time.Unix(120, 0))
			if err != nil {
				panic(err)
			}
			result := query.Exec(context.Background())
			if result.Err != nil {
				panic(result.Err)
			}
			matrix, ok := result.Value.(promql.Matrix)
			if !ok || len(matrix) != 1 || len(matrix[0].Floats) != 5 || matrix[0].Metric.Len() != 0 {
				panic("incorrect top-level subquery result")
			}
			for i, point := range matrix[0].Floats {
				if point.T != int64(i*30000) || point.F != float64(i*30) {
					panic("incorrect top-level subquery timestamp/value")
				}
			}
			query.Close()
		}
	}
	fmt.Println("evaluation-complete:" + os.Args[1])
}
