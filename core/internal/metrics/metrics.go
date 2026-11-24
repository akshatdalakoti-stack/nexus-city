// Package metrics defines the Prometheus metrics each Go service exposes on
// /metrics. Every binary registers only its own group, plus the standard Go
// runtime and process collectors, on a private registry.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const ns = "nexus"

// Registry holds the metrics of this process.
var Registry = newRegistry()

func newRegistry() *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return r
}

// Handler serves the registry in the Prometheus text format.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// ---------------------------------------------------------------- shared

// Published counts envelopes written to each Redis stream by this process.
var Published = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: ns, Name: "stream_published_total", Help: "Messages written to each Redis stream.",
}, []string{"stream"})

// PublishErrors counts failed stream writes.
var PublishErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: ns, Name: "stream_publish_errors_total", Help: "Failed writes to each Redis stream.",
}, []string{"stream"})

// ---------------------------------------------------------------- simulation

var (
	SimSteps = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Subsystem: "sim", Name: "steps_total", Help: "One-second simulation steps executed.",
	})
	SimStepSeconds = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Subsystem: "sim", Name: "step_seconds_total", Help: "Wall time spent executing simulation steps.",
	})
	SimTick = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Subsystem: "sim", Name: "tick", Help: "Current simulation tick (simulated seconds since the epoch).",
	})
	SimSpeed = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Subsystem: "sim", Name: "speed", Help: "Simulated seconds per wall second.",
	})
	SimPaused = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Subsystem: "sim", Name: "paused", Help: "1 while the simulation is paused.",
	})
	SimAgents = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Subsystem: "sim", Name: "vehicle_agents", Help: "Vehicle agents currently on the road (each stands for 8 vehicles).",
	})
)

// RegisterSim registers the simulation worker's metrics.
func RegisterSim() {
	Registry.MustRegister(SimSteps, SimStepSeconds, SimTick, SimSpeed, SimPaused, SimAgents, Published, PublishErrors)
}

// ---------------------------------------------------------------- gateway

var (
	HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Subsystem: "gateway", Name: "http_requests_total", Help: "HTTP requests by route and status code.",
	}, []string{"route", "code"})
	HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Subsystem: "gateway", Name: "http_request_duration_seconds", Help: "HTTP request latency by route (WebSocket excluded).",
		Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 10, 60},
	}, []string{"route"})
	WSClients = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Subsystem: "gateway", Name: "ws_clients", Help: "Connected WebSocket clients.",
	})
	Consumed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Subsystem: "gateway", Name: "stream_consumed_total", Help: "Messages read from each Redis stream.",
	}, []string{"stream"})
)

// RegisterGateway registers the gateway's metrics plus extra collectors
// (for example the scenario queue, read from Redis at scrape time).
func RegisterGateway(extra ...prometheus.Collector) {
	Registry.MustRegister(HTTPRequests, HTTPDuration, WSClients, Consumed, Published, PublishErrors)
	Registry.MustRegister(extra...)
}

// statusRecorder captures the status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer
// (WebSocket upgrades need http.Hijacker).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Instrument records request counts and latency by the matched route
// pattern (never the raw path, so IDs don't explode label cardinality).
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		HTTPRequests.WithLabelValues(route, strconv.Itoa(rec.code)).Inc()
		if route != "GET /ws" {
			HTTPDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
		}
	})
}

// ---------------------------------------------------------------- scenarios

var (
	ScenarioJobs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Subsystem: "scenario", Name: "jobs_total", Help: "Scenarios finished by this worker, by kind and outcome.",
	}, []string{"kind", "status"})
	ScenarioSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Subsystem: "scenario", Name: "job_duration_seconds", Help: "Wall time to run one scenario, including forecasts.",
		Buckets: []float64{.5, 1, 2, 3, 5, 8, 13, 21, 34, 60, 120},
	}, []string{"kind"})
	ScenarioBusy = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Subsystem: "scenario", Name: "busy", Help: "1 while this worker is running a scenario.",
	})
)

// RegisterScenario registers the scenario worker's metrics.
func RegisterScenario() {
	Registry.MustRegister(ScenarioJobs, ScenarioSeconds, ScenarioBusy, Published, PublishErrors)
}

// ---------------------------------------------------------------- recorder

var (
	RecorderWritten = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Subsystem: "recorder", Name: "messages_stored_total", Help: "Stream messages persisted to PostgreSQL, by stream.",
	}, []string{"stream"})
	RecorderErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Subsystem: "recorder", Name: "errors_total", Help: "Messages that could not be persisted.",
	})
)

// RegisterRecorder registers the recorder's metrics.
func RegisterRecorder() {
	Registry.MustRegister(RecorderWritten, RecorderErrors)
}
