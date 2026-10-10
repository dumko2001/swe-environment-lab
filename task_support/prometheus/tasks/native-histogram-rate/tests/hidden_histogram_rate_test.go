package promql_test

import (
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/promqltest"
)

func TestMicro1NativeHistogramExtendedRates(t *testing.T) {
	engine := promqltest.NewTestEngine(t, false, 5*time.Minute, 1_000_000)
	promqltest.RunTest(t, `
load 15s
  hist_counter {{schema:0 count:1 sum:1 buckets:[1] offset:1}}+{{count:1 sum:1 buckets:[1] offset:1}}x7

eval instant at 50s histogram_count(increase(hist_counter[1m] anchored))
  {} 3

eval instant at 50s histogram_count(rate(hist_counter[1m] anchored))
  {} 0.05

eval instant at 50s histogram_count(increase(hist_counter[1m] smoothed))
  {} 3.333333333333333

eval instant at 50s histogram_count(rate(hist_counter[1m] smoothed))
  {} 0.05555555555555555

eval instant at 5s histogram_count(increase(hist_counter[1m] anchored))
  {} 0

eval instant at 5s histogram_sum(increase(hist_counter[1m] anchored))
  {} 0

eval instant at 50s histogram_count(delta(hist_counter[1m] smoothed))
  expect warn msg: PromQL warning: this native histogram metric is not a gauge: "hist_counter"
  {} 3.333333333333333

eval instant at 50s histogram_count(delta(hist_counter[1m] anchored))
  expect warn msg: PromQL warning: this native histogram metric is not a gauge: "hist_counter"
  {} 3

eval range from 0s to 60s step 15s histogram_count(rate(hist_counter[1m] smoothed))
  {} 0 0.016666666666666666 0.03333333333333333 0.05 0.06666666666666667

eval instant at 7s histogram_count(hist_counter smoothed)
  {} 1.4666666666666668

eval instant at 15s histogram_count(hist_counter smoothed)
  {} 2

eval instant at 110s histogram_count(hist_counter smoothed)
  {} 8

clear
load 15s
  custom_only {{schema:-53 sum:1 count:1 custom_values:[5 10] buckets:[1]}}+{{schema:-53 sum:1 count:1 custom_values:[5 10] buckets:[1]}}x7

eval instant at 50s histogram_count(increase(custom_only[1m] smoothed))
  {} 3.333333333333333

eval instant at 50s histogram_sum(increase(custom_only[1m] smoothed))
  {} 3.333333333333333

clear
load 15s
  float_counter 1+1x7

eval instant at 50s increase(float_counter[1m] smoothed)
  {} 3.333333333333333

eval instant at 50s rate(float_counter[1m] anchored)
  {} 0.05

clear
load 30s
  float_reset 5 10 3

eval instant at 1m increase(float_reset[1m] anchored)
  {} 8

clear
load 30s
  gauge_hist {{schema:0 sum:10 count:10 buckets:[10] counter_reset_hint:gauge}} {{schema:0 sum:4 count:4 buckets:[4] counter_reset_hint:gauge}}

eval instant at 30s histogram_count(delta(gauge_hist[30s] anchored))
  {} -6

clear
load 30s
  mid_gauge_hist {{schema:0 sum:1 count:1 buckets:[1]}} {{schema:0 sum:2 count:2 buckets:[2] counter_reset_hint:gauge}} {{schema:0 sum:3 count:3 buckets:[3]}}

eval instant at 90s histogram_count(rate(mid_gauge_hist[90s] anchored))
  expect warn msg: PromQL warning: this native histogram metric is not a counter: "mid_gauge_hist"
  {} 0.022222222222222223

eval instant at 90s histogram_count(increase(mid_gauge_hist[90s] smoothed))
  expect warn msg: PromQL warning: this native histogram metric is not a counter: "mid_gauge_hist"
  {} 2

eval instant at 90s histogram_count(delta(mid_gauge_hist[90s] anchored))
  expect warn msg: PromQL warning: this native histogram metric is not a gauge: "mid_gauge_hist"
  {} 2

clear
load 30s
  reset_custom_hist {{schema:-53 sum:1 count:1 custom_values:[5 10] buckets:[1]}} {{schema:-53 sum:4 count:4 custom_values:[5 10] buckets:[4]}} {{schema:-53 sum:1 count:1 custom_values:[5 10] buckets:[1] counter_reset_hint:reset}} {{schema:-53 sum:3 count:3 custom_values:[5 10] buckets:[3]}}

eval instant at 90s histogram_count(increase(reset_custom_hist[90s] anchored))
  {} 6

eval instant at 90s histogram_sum(increase(reset_custom_hist[90s] anchored))
  {} 6

clear
load 15s
  reset_middle {{schema:0 sum:5 count:5 buckets:[5]}} {{schema:0 sum:10 count:10 buckets:[10]}} {{schema:0 sum:2 count:2 buckets:[2] counter_reset_hint:reset}} {{schema:0 sum:6 count:6 buckets:[6]}}

eval instant at 20s histogram_count(reset_middle smoothed)
  {} 0.6666666666666666

clear
load 30s
  compatible_schema {{schema:1 sum:1 count:1 buckets:[1]}} {{schema:0 sum:3 count:3 buckets:[3]}}

eval instant at 30s histogram_count(increase(compatible_schema[30s] anchored))
  {} 2

eval instant at 30s histogram_sum(increase(compatible_schema[30s] anchored))
  {} 2

eval instant at 30s increase(compatible_schema[30s] anchored)
  {} {{schema:0 count:2 sum:2 counter_reset_hint:gauge buckets:[2]}}

clear
load 30s
  reset_at_end {{schema:0 sum:5 count:5 buckets:[5]}} {{schema:0 sum:10 count:10 buckets:[10]}} {{schema:0 sum:3 count:3 buckets:[3] counter_reset_hint:reset}}

eval instant at 70s histogram_count(increase(reset_at_end[70s] anchored))
  {} 8

clear
load 30s
  two_sample_reset {{schema:0 sum:5 count:5 buckets:[5]}} {{schema:0 sum:3 count:3 buckets:[3] counter_reset_hint:reset}}

eval instant at 30s histogram_count(increase(two_sample_reset[30s] anchored))
  {} 3

clear
load 10s
  double_reset {{schema:0 sum:10 count:10 buckets:[10]}} {{schema:0 sum:5 count:5 buckets:[5] counter_reset_hint:reset}} {{schema:0 sum:2 count:2 buckets:[2] counter_reset_hint:reset}} {{schema:0 sum:4 count:4 buckets:[4]}}

eval instant at 15s histogram_count(increase(double_reset[14s] smoothed))
  {} 5.5

clear
load 20s
  both_interp {{schema:0 sum:10 count:10 buckets:[10]}} {{schema:0 sum:5 count:5 buckets:[5] counter_reset_hint:reset}}

eval instant at 15s histogram_count(increase(both_interp[10s] smoothed))
  {} 2.5

clear
load 10s
  right_reset {{schema:0 sum:5 count:5 buckets:[5]}} {{schema:0 sum:10 count:10 buckets:[10]}} {{schema:0 sum:3 count:3 buckets:[3] counter_reset_hint:reset}}

eval instant at 15s histogram_count(rate(right_reset[10s] smoothed))
  {} 0.4

clear
load 10s
  reset_boundary _ {{schema:0 sum:396 count:396 buckets:[396] counter_reset_hint:not_reset}} {{schema:0 sum:110 count:110 buckets:[110] counter_reset_hint:reset}} {{schema:0 sum:194 count:194 buckets:[194] counter_reset_hint:not_reset}}

eval instant at 20s500ms histogram_count(rate(reset_boundary[1s] smoothed))
  {} 9.7

clear
load 30s
  mixed_schema {{schema:0 sum:1 count:1 buckets:[1]}} {{schema:-53 sum:1 count:1 custom_values:[5 10] buckets:[1]}} {{schema:0 sum:5 count:4 buckets:[1 2 1]}}

eval instant at 45s rate(mixed_schema[1m] smoothed)
  expect warn msg: PromQL warning: vector contains a mix of histograms with exponential and custom buckets schemas for metric name "mixed_schema"

eval instant at 45s histogram_count(mixed_schema smoothed)
  expect warn msg: PromQL warning: vector contains a mix of histograms with exponential and custom buckets schemas for metric name "mixed_schema"

clear
load 20s
  distribution {{schema:0 count:10 sum:20 z_bucket:1 z_bucket_w:0.001 buckets:[2 3] n_buckets:[4]}} {{schema:0 count:20 sum:40 z_bucket:2 z_bucket_w:0.001 buckets:[4 6] n_buckets:[8]}}
  distribution_gauge {{schema:0 count:10 sum:20 z_bucket:1 z_bucket_w:0.001 buckets:[2 3] n_buckets:[4] counter_reset_hint:gauge}} {{schema:0 count:20 sum:40 z_bucket:2 z_bucket_w:0.001 buckets:[4 6] n_buckets:[8] counter_reset_hint:gauge}}
  custom_distribution {{schema:-53 count:10 sum:20 custom_values:[1 5] buckets:[2 3 5]}} {{schema:-53 count:20 sum:40 custom_values:[1 5] buckets:[4 6 10]}}

eval instant at 10s distribution smoothed
  {__name__="distribution"} {{schema:0 count:15 sum:30 z_bucket:1.5 z_bucket_w:0.001 buckets:[3 4.5] n_buckets:[6]}}

eval instant at 15s increase(distribution[10s] smoothed)
  {} {{schema:0 count:5 sum:10 z_bucket:0.5 z_bucket_w:0.001 buckets:[1 1.5] n_buckets:[2] counter_reset_hint:gauge}}

eval instant at 15s rate(distribution[10s] smoothed)
  {} {{schema:0 count:0.5 sum:1 z_bucket:0.05 z_bucket_w:0.001 buckets:[0.1 0.15] n_buckets:[0.2] counter_reset_hint:gauge}}

eval instant at 15s delta(distribution_gauge[10s] smoothed)
  {} {{schema:0 count:5 sum:10 z_bucket:0.5 z_bucket_w:0.001 buckets:[1 1.5] n_buckets:[2] counter_reset_hint:gauge}}

eval instant at 10s custom_distribution smoothed
  {__name__="custom_distribution"} {{schema:-53 count:15 sum:30 custom_values:[1 5] buckets:[3 4.5 7.5]}}

eval instant at 15s increase(custom_distribution[10s] smoothed)
  {} {{schema:-53 count:5 sum:10 custom_values:[1 5] buckets:[1 1.5 2.5] counter_reset_hint:gauge}}

clear
load 30s
  mixed_types 1 2 {{schema:0 sum:3 count:3 buckets:[3]}} {{schema:0 sum:4 count:4 buckets:[4]}}

eval instant at 45s mixed_types smoothed
  expect warn msg: PromQL warning: encountered a mix of histograms and floats for metric name "mixed_types"

eval instant at 45s sort(mixed_types smoothed)
  expect warn msg: PromQL warning: encountered a mix of histograms and floats for metric name "mixed_types"
`, engine)
}
