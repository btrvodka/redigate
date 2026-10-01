// Package metrics exposes Prometheus metrics of the HTTP API and redis commands.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "redigate"

type Collector struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec

	redisCommands *prometheus.CounterVec
	redisDuration *prometheus.HistogramVec
}

func New() *Collector {
	c := &Collector{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "Number of handled HTTP requests.",
		}, []string{"route", "code"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "Duration of HTTP requests.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"route"}),
		redisCommands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "redis_commands_total",
			Help:      "Number of commands sent to redis.",
		}, []string{"command", "status"}),
		redisDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "redis_command_duration_seconds",
			Help:      "Duration of redis commands.",
			Buckets:   []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"command"}),
	}

	c.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		c.httpRequests,
		c.httpDuration,
		c.redisCommands,
		c.redisDuration,
	)

	return c
}

func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{Registry: c.registry})
}

func (c *Collector) ObserveHTTPRequest(route string, code int, duration time.Duration) {
	c.httpRequests.WithLabelValues(route, strconv.Itoa(code)).Inc()
	c.httpDuration.WithLabelValues(route).Observe(duration.Seconds())
}

func (c *Collector) ObserveRedisCommand(command, status string, duration time.Duration) {
	c.redisCommands.WithLabelValues(command, status).Inc()
	c.redisDuration.WithLabelValues(command).Observe(duration.Seconds())
}
