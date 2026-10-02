// Package trace gives each effect function an OpenTelemetry span. It uses the
// global tracer provider, so it records nothing until the program installs
// one.
//
// Generated code calls it like this:
//
//	ctx, span := trace.Start(ctx, "shop.Shop.Checkout")
//	defer trace.End(span, &err)
package trace

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Instrumentation is the instrumentation scope name the spans are reported under.
const Instrumentation = "github.com/effect-go/effect-go"

// Start starts a span named name, as a child of the span in ctx.
func Start(ctx context.Context, name string) (context.Context, oteltrace.Span) {
	return otel.Tracer(Instrumentation).Start(ctx, name)
}

// End ends span, recording *err as the span's error if it isn't nil. Defer it
// directly (defer trace.End(span, &err)): it then also records a panic on the
// span and lets the panic continue, with its original stack.
func End(span oteltrace.Span, err *error) {
	if r := recover(); r != nil {
		span.RecordError(fmt.Errorf("panic: %v", r), oteltrace.WithStackTrace(true))
		span.SetStatus(codes.Error, fmt.Sprint("panic: ", r))
		span.End()
		panic(r)
	}
	if err != nil && *err != nil {
		span.RecordError(*err)
		span.SetStatus(codes.Error, (*err).Error())
	}
	span.End()
}
