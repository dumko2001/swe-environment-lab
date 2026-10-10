package main

import (
	"os"
	"reflect"

	"github.com/prometheus/common/promslog"
	"github.com/prometheus/prometheus/tsdb"
)

// Invoke the repository's existing public-feature configuration path. No field
// name, field index, or representation for histogram enablement is assumed.
func publicStorageOptions(xor2, histogramEncoding bool) *tsdb.Options {
	flags := []string{"st-storage"}
	if xor2 {
		flags = append(flags, "xor2-encoding")
	}
	if histogramEncoding {
		flags = append(flags, "histograms-st-encoding")
	}
	return publicFeatureOptions(flags)
}

func publicFeatureOptions(flags []string) *tsdb.Options {
	cfg := flagConfig{}
	before := cfg.tsdb.ToTSDBOptions()
	cfg.featureList = flags
	if err := cfg.setFeatureListOptions(promslog.NewNopLogger()); err != nil {
		panic(err)
	}
	after := cfg.tsdb.ToTSDBOptions()
	opts := tsdb.DefaultOptions()
	old, configured, target := reflect.ValueOf(before), reflect.ValueOf(after), reflect.ValueOf(opts).Elem()
	for i := 0; i < target.NumField(); i++ {
		if target.Field(i).CanSet() && configured.Field(i).CanInterface() && !reflect.DeepEqual(old.Field(i).Interface(), configured.Field(i).Interface()) {
			target.Field(i).Set(configured.Field(i))
		}
	}
	return opts
}

func init() { micro1StorageProbe(); os.Exit(0) }
