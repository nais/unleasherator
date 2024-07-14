package o11y

import (
	"context"
	"fmt"
	"testing"

	"github.com/nais/unleasherator/pkg/config"
	"github.com/stretchr/testify/assert"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestNewTraceExporter(t *testing.T) {
	ctx := context.Background()

	t.Run("stdout exporter", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter: "stdout",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("otlp exporter with grpc protocol", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter:       "otlp",
				ExporterOtlpEndpoint: "http://localhost:4317",
				ExporterOtlpProtocol: "grpc",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("otlp exporter with secure grpc protocol", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter:       "otlp",
				ExporterOtlpEndpoint: "https://localhost:4317",
				ExporterOtlpProtocol: "grpc",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("otlp exporter with http protocol", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter:       "otlp",
				ExporterOtlpEndpoint: "http://localhost:4317",
				ExporterOtlpProtocol: "http",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("otlp exporter with https protocol", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter:       "otlp",
				ExporterOtlpEndpoint: "https://localhost:4317",
				ExporterOtlpProtocol: "http",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("none exporter", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter: "none",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.NoError(t, err)
		assert.Nil(t, exporter)
	})

	t.Run("unsupported traces exporter", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter: "invalid",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.Error(t, err)
		assert.Nil(t, exporter)
		assert.EqualError(t, err, fmt.Sprintf("unsupported traces exporter %q", cfg.OpenTelemetry.TracesExporter))
	})

	t.Run("unsupported otlp exporter protocol", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter:       "otlp",
				ExporterOtlpProtocol: "invalid",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.Error(t, err)
		assert.Nil(t, exporter)
		assert.EqualError(t, err, fmt.Sprintf("unsupported otlp exporter protocol %q", cfg.OpenTelemetry.ExporterOtlpProtocol))
	})
}

func TestHostportSchemeFromUrl(t *testing.T) {
	tests := []struct {
		name     string
		rawUrl   string
		expected string
	}{
		{
			name:     "HTTP URL without port",
			rawUrl:   "http://example.com",
			expected: "example.com:80, http",
		},
		{
			name:     "HTTPS URL without port",
			rawUrl:   "https://example.com",
			expected: "example.com:443, https",
		},
		{
			name:     "URL with custom port",
			rawUrl:   "http://example.com:8080",
			expected: "example.com:8080, http",
		},
		{
			name:     "URL with custom port and HTTPS scheme",
			rawUrl:   "https://example.com:8443",
			expected: "example.com:8443, https",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hostport, scheme, err := hostportSchemeFromUrl(test.rawUrl)
			assert.NoError(t, err)
			assert.Equal(t, test.expected, fmt.Sprintf("%s, %s", hostport, scheme))
		})
	}
}
func TestResourceFromConfig(t *testing.T) {
	cfg := &config.Config{
		ClusterName:  "test-cluster",
		PodName:      "test-pod",
		PodNamespace: "test-namespace",
	}

	resource, err := resourceFromConfig(cfg)
	assert.NoError(t, err)

	for _, attribute := range resource.Attributes() {
		switch attribute.Key {
		case semconv.ServiceNameKey:
			assert.Equal(t, "unleasherator-test-cluster", attribute.Value.AsString())
		case semconv.ServiceInstanceIDKey:
			assert.Equal(t, "test-pod", attribute.Value.AsString())
		case semconv.ServiceNamespaceKey:
			assert.Equal(t, "test-namespace", attribute.Value.AsString())
		case semconv.K8SClusterNameKey:
			assert.Equal(t, "test-cluster", attribute.Value.AsString())
		case semconv.TelemetrySDKNameKey:
		case semconv.TelemetrySDKLanguageKey:
		case semconv.TelemetrySDKVersionKey:
		default:
			assert.Fail(t, fmt.Sprintf("unexpected attribute %q", attribute.Key))
		}
	}
}
