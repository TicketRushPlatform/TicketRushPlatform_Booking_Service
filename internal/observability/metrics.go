package observability

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const serviceName = "booking_api"

type Metrics struct {
	registry        *prometheus.Registry
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	requestSize     *prometheus.HistogramVec
	responseSize    *prometheus.HistogramVec
	inFlight        prometheus.Gauge
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ticketrush",
			Subsystem: serviceName,
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests.",
		}, []string{"method", "route", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "ticketrush",
			Subsystem: serviceName,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency in seconds.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route", "status"}),
		requestSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "ticketrush",
			Subsystem: serviceName,
			Name:      "http_request_size_bytes",
			Help:      "HTTP request size in bytes.",
			Buckets:   prometheus.ExponentialBuckets(100, 2, 12),
		}, []string{"method", "route"}),
		responseSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "ticketrush",
			Subsystem: serviceName,
			Name:      "http_response_size_bytes",
			Help:      "HTTP response size in bytes.",
			Buckets:   prometheus.ExponentialBuckets(100, 2, 12),
		}, []string{"method", "route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "ticketrush",
			Subsystem: serviceName,
			Name:      "http_requests_in_flight",
			Help:      "Current number of HTTP requests being served.",
		}),
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requestsTotal,
		m.requestDuration,
		m.requestSize,
		m.responseSize,
		m.inFlight,
	)

	return m
}

func (m *Metrics) Handler() gin.HandlerFunc {
	return gin.WrapH(promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
}

func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}

		start := time.Now()
		m.inFlight.Inc()
		defer m.inFlight.Dec()

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		method := c.Request.Method
		status := strconv.Itoa(c.Writer.Status())

		m.requestsTotal.WithLabelValues(method, route, status).Inc()
		m.requestDuration.WithLabelValues(method, route, status).Observe(time.Since(start).Seconds())
		m.requestSize.WithLabelValues(method, route).Observe(float64(computeApproximateRequestSize(c)))
		if c.Writer.Size() >= 0 {
			m.responseSize.WithLabelValues(method, route).Observe(float64(c.Writer.Size()))
		}
	}
}

func computeApproximateRequestSize(c *gin.Context) int64 {
	size := c.Request.ContentLength
	if size < 0 {
		size = 0
	}
	size += int64(len(c.Request.Method) + len(c.Request.URL.String()) + len(c.Request.Proto))
	for name, values := range c.Request.Header {
		size += int64(len(name))
		for _, value := range values {
			size += int64(len(value))
		}
	}
	return size
}
