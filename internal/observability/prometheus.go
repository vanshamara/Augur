package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// NewPrometheus builds an Observer whose metrics can be scraped and returns an
// HTTP handler that serves them. The observer and handler share a private
// registry, so callers and tests do not collide on the global one.
func NewPrometheus(name string) (*Observer, http.Handler, error) {
	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return nil, nil, err
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	observer := New(Config{Name: name, MeterProvider: provider})
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return observer, handler, nil
}
