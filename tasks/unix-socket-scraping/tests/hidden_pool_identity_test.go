package scrape

import (
	"context"
	"encoding/base64"
	"fmt"
	config_util "github.com/prometheus/common/config"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery/targetgroup"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/prometheus/prometheus/util/teststorage"
)

// Exercise the actual socket and TCP connection pools, including transport wrappers.
func micro1PoolServer(t *testing.T, listener net.Listener, origin string) *atomic.Int64 {
	t.Helper()
	accepts := &atomic.Int64{}
	server := &http.Server{
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				accepts.Add(1)
			}
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			fmt.Fprintf(w, "%s|%s", origin, r.Header.Get("Authorization"))
		}),
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return accepts
}

func TestMicro1UnixSocketPoolIdentity(t *testing.T) {
	dir, err := os.MkdirTemp("", "pool-observation-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sockets := []string{filepath.Join(dir, "shared.sock"), filepath.Join(dir, "other.sock")}
	counts := map[string]*atomic.Int64{}
	for index, name := range []string{"shared", "other"} {
		listener, err := net.Listen("unix", sockets[index])
		require.NoError(t, err)
		counts[name] = micro1PoolServer(t, listener, name)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	authority := listener.Addr().String()
	counts["tcp"] = micro1PoolServer(t, listener, "tcp")

	cfg := &config.ScrapeConfig{
		JobName:                    "unix-socket-pool-identity",
		Scheme:                     "http",
		MetricsPath:                "/metrics",
		ScrapeInterval:             model.Duration(time.Hour),
		ScrapeTimeout:              model.Duration(time.Second),
		MetricNameValidationScheme: model.UTF8Validation,
		RelabelConfigs: []*relabel.Config{
			{
				Action:               relabel.Replace,
				SourceLabels:         model.LabelNames{"__meta_unix_socket"},
				Regex:                relabel.MustNewRegexp("(.*)"),
				TargetLabel:          "__unix_socket__",
				Replacement:          "$1",
				NameValidationScheme: model.UTF8Validation,
			},
			{
				Action:               relabel.Replace,
				SourceLabels:         model.LabelNames{"__meta_slot"},
				Regex:                relabel.MustNewRegexp("(.*)"),
				TargetLabel:          "target_slot",
				Replacement:          "$1",
				NameValidationScheme: model.UTF8Validation,
			},
		},
	}

	cfg.HTTPClientConfig.BasicAuth = &config_util.BasicAuth{Username: "before", Password: "secret"}

	sp, err := newScrapePool(
		cfg,
		teststorage.NewAppendable(),
		nil,
		0,
		nil,
		nil,
		&Options{skipJitterOffsetting: true},
		newTestScrapeMetrics(t),
	)
	require.NoError(t, err)
	t.Cleanup(sp.stop)

	scrapers := map[string]*targetScraper{}
	sp.injectTestNewLoop = func(options scrapeLoopOptions) loop {
		scrapers[options.target.labels.Get("target_slot")] = options.scraper.(*targetScraper)
		return noopLoop()
	}
	sp.Sync([]*targetgroup.Group{{Targets: []model.LabelSet{
		{model.AddressLabel: model.LabelValue(authority), "__meta_unix_socket": model.LabelValue(sockets[0]), "__meta_slot": "shared-a"},
		{model.AddressLabel: model.LabelValue(authority), "__meta_unix_socket": model.LabelValue(sockets[0]), "__meta_slot": "shared-b"},
		{model.AddressLabel: model.LabelValue(authority), "__meta_unix_socket": model.LabelValue(sockets[1]), "__meta_slot": "other"},
		{model.AddressLabel: model.LabelValue(authority), "__meta_slot": "tcp"},
	}}})

	observe := func(username string) {
		t.Helper()
		require.Len(t, scrapers, 4)
		for round := 0; round < 3; round++ {
			for _, slot := range []string{"shared-a", "shared-b", "other", "tcp"} {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				response, err := scrapers[slot].scrape(ctx)
				require.NoError(t, err)
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				cancel()
				origin := slot
				if slot == "shared-a" || slot == "shared-b" {
					origin = "shared"
				}
				want := origin + "|Basic " + base64.StdEncoding.EncodeToString([]byte(username+":secret"))
				require.Equal(t, want, string(body), "socket/TCP routing or HTTP configuration was not respected")
			}
		}
	}
	observe("before")
	for name, count := range counts {
		require.Equal(t, int64(1), count.Load(), "%s: sequential targets should reuse the available connection", name)
	}
	before := map[string]int64{}
	for name, count := range counts {
		before[name] = count.Load()
	}
	scrapers = map[string]*targetScraper{}
	reloaded := *cfg
	reloaded.HTTPClientConfig = cfg.HTTPClientConfig
	reloaded.HTTPClientConfig.BasicAuth = &config_util.BasicAuth{Username: "after", Password: "secret"}
	require.NoError(t, sp.reload(&reloaded))
	observe("after")
	for name, count := range counts {
		// Reload may replace the pool or safely reuse it with updated request wrappers.
		require.LessOrEqual(t, count.Load()-before[name], int64(1), "%s: reload disabled effective connection reuse", name)
	}
}

// Options.HTTPClientOptions is the exported hook used by embedding callers.
// A socket-specific default must preserve the caller's supplied dial function.
func TestMicro1UnixSocketCallerDialOption(t *testing.T) {
	dir, err := os.MkdirTemp("", "socket-option-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "exporter.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	micro1PoolServer(t, listener, "configured")
	var calls atomic.Int64
	cfg := config.DefaultScrapeConfig
	cfg.JobName = "socket-option"
	cfg.ScrapeInterval = model.Duration(time.Hour)
	cfg.ScrapeTimeout = model.Duration(time.Second)
	cfg.MetricNameValidationScheme = model.UTF8Validation
	options := &Options{HTTPClientOptions: []config_util.HTTPClientOption{
		config_util.WithDialContextFunc(func(ctx context.Context, _, _ string) (net.Conn, error) {
			calls.Add(1)
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}),
	}}
	sp, err := newScrapePool(&cfg, teststorage.NewAppendable(), nil, 0, nil, nil, options, newTestScrapeMetrics(t))
	require.NoError(t, err)
	t.Cleanup(sp.stop)
	var scraper *targetScraper
	sp.injectTestNewLoop = func(options scrapeLoopOptions) loop {
		scraper = options.scraper.(*targetScraper)
		return noopLoop()
	}
	sp.Sync([]*targetgroup.Group{{Targets: []model.LabelSet{{
		model.AddressLabel: "localhost:8080", "__unix_socket__": model.LabelValue(socket),
	}}}})
	require.NotNil(t, scraper)
	response, err := scraper.scrape(context.Background())
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "configured|", string(body))
	require.Greater(t, calls.Load(), int64(0), "caller HTTP dial option was discarded")
}
