package o11y

import (
	"context"
	"fmt"

	"github.com/nais/unleasherator/pkg/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

func InitTracing(ctx context.Context, config *config.Config) (*sdktrace.TracerProvider, error) {
	var exp sdktrace.SpanExporter
	var tp *sdktrace.TracerProvider
	var err error

	switch config.OpenTelemetry.TracesExporter {
	case "stdout":
		exp, err = newStdoutTraceExporter(ctx)
	case "otlp":
		switch config.OpenTelemetry.ExporterOtelpProtocol {
		case "grpc":
			exp, err = newOtelpGrpcTraceExporter(ctx, config)
		case "http":
			exp, err = newOtelpHttpTraceExporter(ctx, config)
		default:
			err = fmt.Errorf("unsupported otelp exporter protocol %q", config.OpenTelemetry.ExporterOtelpProtocol)
		}
	case "none":
	default:
		err = fmt.Errorf("unsupported traces exporter %q", config.OpenTelemetry.TracesExporter)
	}

	if err != nil {
		return nil, err
	}

	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("Unleasherator"),
		),
	)

	if err != nil {
		return nil, err
	}

	if config.OpenTelemetry.TracesExporter == "none" {
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(r),
		)
	} else {
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(r),
		)
	}

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return tp, nil
}

func newStdoutTraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	return stdouttrace.New(stdouttrace.WithPrettyPrint())
}

func newOtelpGrpcTraceExporter(ctx context.Context, config *config.Config) (sdktrace.SpanExporter, error) {
	return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(config.OpenTelemetry.ExporterOtelpEndpoint))
}

func newOtelpHttpTraceExporter(ctx context.Context, config *config.Config) (sdktrace.SpanExporter, error) {
	return otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(config.OpenTelemetry.ExporterOtelpEndpoint))
}
