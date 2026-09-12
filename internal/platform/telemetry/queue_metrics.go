package telemetry

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// QueueMetricsSnapshot contains the bounded aggregate values exposed for durable work.
type QueueMetricsSnapshot struct {
	OutboxUndispatched float64
	OutboxLagSeconds   float64
	JobDepth           map[string]float64
}

// QueueMetricsSource reads durable-work aggregates. It must not return payloads,
// IDs, handlers, event types, or other high-cardinality values.
type QueueMetricsSource interface {
	QueueMetrics(context.Context) (QueueMetricsSnapshot, error)
}

type queueMetricsCollector struct {
	source  QueueMetricsSource
	lag     *prometheus.Desc
	pending *prometheus.Desc
	depth   *prometheus.Desc
	up      *prometheus.Desc
}

func newQueueMetricsCollector(source QueueMetricsSource) prometheus.Collector {
	return &queueMetricsCollector{
		source:  source,
		lag:     prometheus.NewDesc("outbox_dispatch_lag_seconds", "Age of the oldest undispatched outbox event", nil, nil),
		pending: prometheus.NewDesc("outbox_undispatched_events", "Number of undispatched outbox events", nil, nil),
		depth:   prometheus.NewDesc("job_queue_depth", "Number of durable jobs by lifecycle status", []string{"status"}, nil),
		up:      prometheus.NewDesc("queue_metrics_up", "Whether the most recent durable queue metrics query succeeded", nil, nil),
	}
}

func (c *queueMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.lag
	ch <- c.pending
	ch <- c.depth
	ch <- c.up
}

func (c *queueMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := c.source.QueueMetrics(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(c.pending, prometheus.GaugeValue, snapshot.OutboxUndispatched)
	ch <- prometheus.MustNewConstMetric(c.lag, prometheus.GaugeValue, snapshot.OutboxLagSeconds)
	for _, status := range []string{"pending", "running", "completed", "dead", "cancelled"} {
		ch <- prometheus.MustNewConstMetric(c.depth, prometheus.GaugeValue, snapshot.JobDepth[status], status)
	}
}
