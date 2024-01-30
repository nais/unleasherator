package o11y

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func ReconcilerAttributes(ctx context.Context, req ctrl.Request) []trace.SpanStartOption {
	id := controller.ReconcileIDFromContext(ctx)

	return []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("reconcile.id", string(id)),
			attribute.String("reconcile.namespace", req.Namespace),
			attribute.String("reconcile.name", req.Name),
		),
	}
}
