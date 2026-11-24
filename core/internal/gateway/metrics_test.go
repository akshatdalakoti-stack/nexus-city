package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"nexuscity/internal/metrics"
)

func TestHTTPMetricsUseRoutePatterns(t *testing.T) {
	h := newHarness(t)
	before := testutil.ToFloat64(metrics.HTTPRequests.WithLabelValues("GET /api/history/{kind}/{id}", "404"))
	for _, id := range []string{"R-nope-1", "R-nope-2"} {
		resp, err := http.Get(h.http.URL + "/api/history/road/" + id)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	after := testutil.ToFloat64(metrics.HTTPRequests.WithLabelValues("GET /api/history/{kind}/{id}", "404"))
	if after-before != 2 {
		t.Fatalf("expected 2 requests under the route pattern, got %v", after-before)
	}
	// Raw paths must never become labels.
	n, err := testutil.GatherAndCount(regWith(metrics.HTTPRequests), "nexus_gateway_http_requests_total")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan prometheus.Metric, n)
	metrics.HTTPRequests.Collect(ch)
	close(ch)
	for m := range ch {
		if strings.Contains(m.Desc().String(), "R-nope") {
			t.Fatalf("raw path leaked into labels: %s", m.Desc())
		}
	}
}

func TestQueueCollectorReadsRedis(t *testing.T) {
	h := newHarness(t)
	reg := regWith(h.srv.QueueCollector())
	want := `
# HELP nexus_scenario_queue_up 1 if the queue could be read from Redis.
# TYPE nexus_scenario_queue_up gauge
nexus_scenario_queue_up 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "nexus_scenario_queue_up"); err != nil {
		t.Fatal(err)
	}
}

func regWith(cs ...prometheus.Collector) *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(cs...)
	return r
}
