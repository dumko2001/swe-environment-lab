package scrape

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery/targetgroup"
	"github.com/prometheus/prometheus/util/teststorage"
	"github.com/stretchr/testify/require"
)

func udsWeakRequest(t *testing.T, scraper *targetScraper, want string) {
	t.Helper()
	response, err := scraper.scrape(context.Background())
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, want+"|", string(body))
}

// A still-live shared allocation may contain a retired client value whose
// references have been cleared. Scalar timeout values do not retain socket-specific state.
func udsWeakStateReleased(client *http.Client, active map[string]*targetScraper) bool {
	if client == nil {
		return true
	}
	if client.Transport == nil && client.Jar == nil && client.CheckRedirect == nil {
		return true
	}
	// Sharing the same transport with a live target preserves intentionally
	// shared connection state; do not demand per-socket client allocation.
	if client.Transport != nil && client.Jar == nil && client.CheckRedirect == nil {
		typ := reflect.TypeOf(client.Transport)
		if typ.Comparable() {
			for _, s := range active {
				if client.Transport == s.client.Transport {
					return true
				}
			}
		}
	}
	return false
}

func udsWeakCollect(retired []weak.Pointer[http.Client], active map[string]*targetScraper) int {
	allowed := map[weak.Pointer[http.Client]]bool{}
	for _, s := range active {
		allowed[weak.Make(s.client)] = true
	}
	runtime.GC()
	distinct := map[weak.Pointer[http.Client]]bool{}
	for _, w := range retired {
		if !allowed[w] && !udsWeakStateReleased(w.Value(), active) {
			distinct[w] = true
		}
	}
	runtime.KeepAlive(active)
	return len(distinct)
}

func udsWeakSentinel() weak.Pointer[http.Client] {
	return weak.Make(&http.Client{Timeout: time.Second})
}

func TestMicro1UnixSocketClientRetirement(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "uds-weak-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	activeSocket := filepath.Join(dir, "active.sock")
	listener, err := net.Listen("unix", activeSocket)
	require.NoError(t, err)
	micro1PoolServer(t, listener, "active")
	cfg := config.DefaultScrapeConfig
	cfg.JobName = "weak-client-churn"
	cfg.ScrapeInterval = model.Duration(time.Hour)
	cfg.ScrapeTimeout = model.Duration(time.Second)
	cfg.MetricNameValidationScheme = model.UTF8Validation
	sp, err := newScrapePool(&cfg, teststorage.NewAppendable(), nil, 0, nil, nil, &Options{}, newTestScrapeMetrics(t))
	require.NoError(t, err)
	t.Cleanup(sp.stop)
	scrapers := map[string]*targetScraper{}
	sp.injectTestNewLoop = func(o scrapeLoopOptions) loop {
		scrapers[o.target.labels.Get("slot")] = o.scraper.(*targetScraper)
		return noopLoop()
	}
	activeTargets := []model.LabelSet{{model.AddressLabel: "localhost:8080", "__unix_socket__": model.LabelValue(activeSocket), "slot": "active-a"}, {model.AddressLabel: "localhost:8080", "__unix_socket__": model.LabelValue(activeSocket), "slot": "active-b"}}
	sp.Sync([]*targetgroup.Group{{Targets: activeTargets}})
	udsWeakRequest(t, scrapers["active-a"], "active")
	udsWeakRequest(t, scrapers["active-b"], "active")
	var retired []weak.Pointer[http.Client]
	total := 0
	for _, wave := range []int{8, 16, 32} {
		for i := 0; i < wave; i++ {
			total++
			slot := fmt.Sprintf("retired-%d", total)
			socket := filepath.Join(dir, slot+".sock")
			l, err := net.Listen("unix", socket)
			require.NoError(t, err)
			micro1PoolServer(t, l, slot)
			targets := append(append([]model.LabelSet{}, activeTargets...), model.LabelSet{model.AddressLabel: "localhost:8080", "__unix_socket__": model.LabelValue(socket), "slot": model.LabelValue(slot)})
			sp.Sync([]*targetgroup.Group{{Targets: targets}})
			udsWeakRequest(t, scrapers[slot], slot)
			retired = append(retired, weak.Make(scrapers[slot].client))
			sp.Sync([]*targetgroup.Group{{Targets: activeTargets}})
			delete(scrapers, slot)
		}
		sentinel := udsWeakSentinel()
		retained := udsWeakCollect(retired, scrapers)
		require.Nil(t, sentinel.Value(), "VERIFIER_RUNTIME_CALIBRATION_ERROR: runtime did not clear an unreferenced ordinary http.Client; do not classify as candidate retention")
		t.Logf("removed_socket_targets=%d retained_inactive_distinct_clients=%d", total, retained)
		require.Zero(t, retained, "removed targets retained otherwise-unused HTTP client state")
		udsWeakRequest(t, scrapers["active-a"], "active")
		udsWeakRequest(t, scrapers["active-b"], "active")
	}
	// Removing one of two targets using a socket must preserve the other.
	sp.Sync([]*targetgroup.Group{{Targets: activeTargets[1:]}})
	delete(scrapers, "active-a")
	runtime.GC()
	udsWeakRequest(t, scrapers["active-b"], "active")
	runtime.KeepAlive(sp)
}
