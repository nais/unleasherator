package o11y

import (
	"context"
	"fmt"
	"net/url"

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
	var tp *sdktrace.TracerProvider

	exp, err := newTraceExporter(ctx, config)

	if err != nil {
		return nil, err
	}

	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("Unleasherator"),
			semconv.ServiceInstanceID(config.PodName),
			semconv.ServiceNamespace(config.PodNamespace),
			semconv.K8SClusterName(config.ClusterName),
		),
	)

	if err != nil {
		return nil, err
	}

	tp = sdktrace.NewTracerProvider(
		sdktrace.WithResource(r),
	)

	if config.OpenTelemetry.TracesExporter != "none" {
		tp.RegisterSpanProcessor(sdktrace.NewBatchSpanProcessor(exp))
	}

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return tp, nil
}

func newTraceExporter(ctx context.Context, config *config.Config) (sdktrace.SpanExporter, error) {
	switch config.OpenTelemetry.TracesExporter {
	case "stdout":
		return newStdoutTraceExporter(ctx)
	case "otlp":
		switch config.OpenTelemetry.ExporterOtlpProtocol {
		case "grpc":
			return newOtlpGrpcTraceExporter(ctx, config)
		case "http":
			return newOtlpHttpTraceExporter(ctx, config)
		default:
			return nil, fmt.Errorf("unsupported otlp exporter protocol %q", config.OpenTelemetry.ExporterOtlpProtocol)
		}
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported traces exporter %q", config.OpenTelemetry.TracesExporter)
	}
}

func newStdoutTraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	return stdouttrace.New(stdouttrace.WithPrettyPrint())
}

func hostportSchemeFromUrl(rawUrl string) (string, string, error) {
	url, err := url.Parse(rawUrl)
	if err != nil {
		return "", "", err
	}

	port := url.Port()
	if port == "" {
		if url.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return fmt.Sprintf("%s:%s", url.Hostname(), port), url.Scheme, nil
}

func newOtlpGrpcTraceExporter(ctx context.Context, config *config.Config) (sdktrace.SpanExporter, error) {
	hostport, scheme, err := hostportSchemeFromUrl(config.OpenTelemetry.ExporterOtlpEndpoint)
	if err != nil {
		return nil, err
	}

	if scheme == "http" {
		return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(hostport), otlptracegrpc.WithInsecure())
	} else {
		return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(hostport))
	}
}

func newOtlpHttpTraceExporter(ctx context.Context, config *config.Config) (sdktrace.SpanExporter, error) {
	hostport, scheme, err := hostportSchemeFromUrl(config.OpenTelemetry.ExporterOtlpEndpoint)
	if err != nil {
		return nil, err
	}

	if scheme == "http" {
		return otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(hostport), otlptracehttp.WithInsecure())
	} else {
		return otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(hostport))
	}
}
