// Package metrics provides monitoring of the GitHub related metrics.
//
// This depends on the metrics exporter of kubebuilder.
// See https://book.kubebuilder.io/reference/metrics.html for details.
package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func init() {
	metrics.Registry.MustRegister(metricRateLimit, metricRateLimitRemaining)
}

var (
	// https://docs.github.com/en/rest/overview/resources-in-the-rest-api#rate-limiting
	metricRateLimit = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "github_rate_limit",
			Help: "The maximum number of requests you're permitted to make per hour",
		},
	)
	metricRateLimitRemaining = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "github_rate_limit_remaining",
			Help: "The number of requests remaining in the current rate limit window",
		},
	)
)

const (
	// https://docs.github.com/en/rest/overview/resources-in-the-rest-api#rate-limiting
	headerRateLimit          = "X-RateLimit-Limit"
	headerRateLimitRemaining = "X-RateLimit-Remaining"
)

// Transport wraps a transport with metrics monitoring
type Transport struct {
	Transport http.RoundTripper
}

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	if resp != nil {
		parseResponse(resp)
	}
	return resp, err
}

func parseResponse(resp *http.Response) {
	rateLimit, err := strconv.Atoi(resp.Header.Get(headerRateLimit))
	if err == nil {
		metricRateLimit.Set(float64(rateLimit))
	}
	rateLimitRemaining, err := strconv.Atoi(resp.Header.Get(headerRateLimitRemaining))
	if err == nil {
		metricRateLimitRemaining.Set(float64(rateLimitRemaining))
	}
}
