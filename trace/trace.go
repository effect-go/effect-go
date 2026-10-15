// Package trace gives each effect function an OpenTelemetry span. A span
// under a recording span comes from that span's tracer provider, and other
// spans from the global one, so nothing is recorded until the program
// installs a provider.
//
// Generated code calls it like this:
//
//	ctx, span := trace.Start(ctx, "shop.Shop.Checkout")
//	defer trace.End(span, &err)
package trace

import (
	"context"
	"errors"
	"fmt"

	"github.com/effect-go/effect-go/scope"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Instrumentation is the instrumentation scope name the spans are reported under.
const Instrumentation = "github.com/effect-go/effect-go"

// Start starts a span named name, as a child of the span in ctx, from that
// span's tracer provider if it is recording, or else from the global one.
func Start(ctx context.Context, name string) (context.Context, oteltrace.Span) {
	if p := oteltrace.SpanFromContext(ctx); p.IsRecording() {
		return p.TracerProvider().Tracer(Instrumentation).Start(ctx, name)
	}
	return otel.Tracer(Instrumentation).Start(ctx, name)
}

// End ends span, recording *err as the span's error if it isn't nil; a
// cancellation (context.Canceled, and in a joined error, every part an
// interruption) is an "interrupted" event instead, as the span didn't fail.
// Defer it directly (defer trace.End(span, &err)): it then also records a
// panic on the span and lets the panic continue, with its original stack.
func End(span oteltrace.Span, err *error) {
	if r := recover(); r != nil {
		// A task's panic comes back as a *scope.Panic, with the stack of
		// the task's goroutine rather than this one.
		val, stack := r, oteltrace.EventOption(oteltrace.WithStackTrace(true))
		if p, ok := r.(*scope.Panic); ok {
			val, stack = p.Value, oteltrace.WithAttributes(attribute.String("exception.stacktrace", string(p.Stack)))
		}
		span.RecordError(fmt.Errorf("panic: %v", val), stack)
		span.SetStatus(codes.Error, fmt.Sprint("panic: ", val))
		span.End()
		panic(r)
	}
	switch {
	case err == nil || *err == nil:
	case errors.Is(*err, context.Canceled) && scope.KindOf(*err) == scope.Interrupt:
		// Interrupted, as a race's loser or a failed task's sibling: not
		// a failure of this span's own.
		span.AddEvent("interrupted", oteltrace.WithAttributes(attribute.String("reason", (*err).Error())))
	default:
		span.RecordError(*err)
		span.SetStatus(codes.Error, (*err).Error())
	}
	span.End()
}
