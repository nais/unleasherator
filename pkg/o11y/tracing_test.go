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
				TracesExporter:        "otlp",
				ExporterOtelpEndpoint: "localhost:4317",
				ExporterOtelpProtocol: "grpc",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("otlp exporter with http protocol", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter:        "otlp",
				ExporterOtelpEndpoint: "localhost:4317",
				ExporterOtelpProtocol: "http",
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

	t.Run("unsupported otelp exporter protocol", func(t *testing.T) {
		cfg := &config.Config{
			OpenTelemetry: config.OpenTelemetryConfig{
				TracesExporter:        "otlp",
				ExporterOtelpProtocol: "invalid",
			},
		}

		exporter, err := newTraceExporter(ctx, cfg)

		assert.Error(t, err)
		assert.Nil(t, exporter)
		assert.EqualError(t, err, fmt.Sprintf("unsupported otelp exporter protocol %q", cfg.OpenTelemetry.ExporterOtelpProtocol))
	})
}
