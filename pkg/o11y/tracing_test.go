package o11y

import (
	"context"
	"fmt"
	"testing"

	"github.com/nais/unleasherator/pkg/config"
	"github.com/stretchr/testify/assert"
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
