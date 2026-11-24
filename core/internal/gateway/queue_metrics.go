package gateway

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// queueCollector reports the scenario queue from Redis at scrape time, so
// Prometheus sees the same numbers KEDA scales on.
type queueCollector struct {
	s                         *Server
	waiting, running, workers *prometheus.Desc
	up                        *prometheus.Desc
}

// QueueCollector returns a Prometheus collector for the scenario queue.
func (s *Server) QueueCollector() prometheus.Collector {
	d := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc("nexus_scenario_queue_"+name, help, nil, nil)
	}
	return &queueCollector{s: s,
		waiting: d("waiting", "Scenarios queued but not yet picked up (consumer-group lag)."),
		running: d("running", "Scenarios delivered to a worker and not yet acknowledged."),
		workers: d("consumers", "Workers registered in the consumer group (including ones that have exited)."),
		up:      d("up", "1 if the queue could be read from Redis."),
	}
}

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.waiting
	ch <- c.running
	ch <- c.workers
	ch <- c.up
}

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	q, err := c.s.queueInfo(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(c.waiting, prometheus.GaugeValue, float64(q.Waiting))
	ch <- prometheus.MustNewConstMetric(c.running, prometheus.GaugeValue, float64(q.Running))
	ch <- prometheus.MustNewConstMetric(c.workers, prometheus.GaugeValue, float64(q.Consumers))
}
