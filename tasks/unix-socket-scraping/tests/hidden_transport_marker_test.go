package scrape

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	config_util "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery/targetgroup"
	"github.com/prometheus/prometheus/util/teststorage"
	"github.com/stretchr/testify/require"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"
)

type udsTransportMarker struct{ payload [1024]byte }

func udsTransportMarkerSentinel() weak.Pointer[udsTransportMarker] {
	return weak.Make(&udsTransportMarker{})
}

func TestMicro1UnixSocketTransportMarkerRetirement(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "uds-weak-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	// Use an ordinary trusted TLS fixture so lazy TLS construction is valid.
	tlsFixture := httptest.NewTLSServer(nil)
	serverTLS := tlsFixture.TLS.Clone()
	serverTLS.ClientAuth = tls.RequestClientCert
	certificate := tlsFixture.Certificate()
	tlsFixture.Close()
	activeSocket := filepath.Join(dir, "active.sock")
	listener, err := net.Listen("unix", activeSocket)
	require.NoError(t, err)
	micro1PoolServer(t, tls.NewListener(listener, serverTLS), "active")
	cfg := config.DefaultScrapeConfig
	cfg.JobName = "weak-client-churn"
	cfg.Scheme = "https"
	cfg.HTTPClientConfig.TLSConfig.CA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}))
	cfg.HTTPClientConfig.TLSConfig.ServerName = certificate.DNSNames[0]
	cfg.ScrapeInterval = model.Duration(time.Hour)
	cfg.ScrapeTimeout = model.Duration(time.Second)
	cfg.MetricNameValidationScheme = model.UTF8Validation
	var tlsHookCalls atomic.Int64
	var markerMu sync.Mutex
	var markers []weak.Pointer[udsTransportMarker]
	options := &Options{HTTPClientOptions: []config_util.HTTPClientOption{
		config_util.WithKeepAlivesDisabled(),
		config_util.WithNewTLSConfigFunc(func(ctx context.Context, cfg *config_util.TLSConfig, opts ...config_util.TLSConfigOption) (*tls.Config, error) {
			tlsConfig, err := config_util.NewTLSConfigWithContext(ctx, cfg, opts...)
			if err != nil {
				return nil, err
			}
			marker := &udsTransportMarker{}
			markerMu.Lock()
			markers = append(markers, weak.Make(marker))
			markerMu.Unlock()
			// The callback is carried by the public TLS configuration into a transport.
			// The server requests (but does not require) a client certificate,
			// proving the public TLS hook is actually used for HTTPS.
			tlsConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				tlsHookCalls.Add(1)
				runtime.KeepAlive(marker)
				return &tls.Certificate{}, nil
			}
			return tlsConfig, nil
		}),
	}}
	liveMarkers := func() int {
		sentinel := udsTransportMarkerSentinel()
		runtime.GC()
		require.Nil(t, sentinel.Value(), "VERIFIER_RUNTIME_CALIBRATION_ERROR: unreferenced transport marker survived GC")
		markerMu.Lock()
		defer markerMu.Unlock()
		live := 0
		for _, w := range markers {
			if w.Value() != nil {
				live++
			}
		}
		return live
	}
	sp, err := newScrapePool(&cfg, teststorage.NewAppendable(), nil, 0, nil, nil, options, newTestScrapeMetrics(t))
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
	baseline := liveMarkers()
	require.Greater(t, tlsHookCalls.Load(), int64(0), "supplied TLS configuration callback was not used")
	total := 0
	for _, wave := range []int{8, 16, 32} {
		for i := 0; i < wave; i++ {
			total++
			slot := fmt.Sprintf("retired-%d", total)
			socket := filepath.Join(dir, slot+".sock")
			l, err := net.Listen("unix", socket)
			require.NoError(t, err)
			micro1PoolServer(t, tls.NewListener(l, serverTLS), slot)
			targets := append(append([]model.LabelSet{}, activeTargets...), model.LabelSet{model.AddressLabel: "localhost:8080", "__unix_socket__": model.LabelValue(socket), "slot": model.LabelValue(slot)})
			sp.Sync([]*targetgroup.Group{{Targets: targets}})
			udsWeakRequest(t, scrapers[slot], slot)

			sp.Sync([]*targetgroup.Group{{Targets: activeTargets}})
			delete(scrapers, slot)
		}
		retained := liveMarkers()
		// Synchronization may enqueue asynchronous disposal. Allow its work and
		// request goroutines to finish before treating retained state as a leak.
		deadline := time.Now().Add(5 * time.Second)
		for retained > baseline && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
			retained = liveMarkers()
		}
		t.Logf("removed_socket_targets=%d retained_tls_markers=%d baseline_active_markers=%d", total, retained, baseline)
		require.LessOrEqual(t, retained, baseline, "removed socket targets retained transport-owned TLS configuration")
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
