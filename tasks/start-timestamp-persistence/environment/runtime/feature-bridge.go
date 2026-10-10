// Public development adapter to the repository's existing feature parser.
package main

import (
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/prometheus/tsdb"
	"os"
	"reflect"
)

func publicStorageOptions(features []string) *tsdb.Options {
	cfg := flagConfig{}
	before := cfg.tsdb.ToTSDBOptions()
	cfg.featureList = append([]string{"st-storage"}, features...)
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
func init() { publicStorageDemo(); os.Exit(0) }
