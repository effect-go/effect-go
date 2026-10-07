package trace

import (
	"context"
	"log/slog"
	"slices"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// LogHandler wraps h so that records logged with a context holding a span
// carry its trace_id and span_id, which link each log line to its trace:
//
//	slog.SetDefault(slog.New(trace.LogHandler(slog.NewJSONHandler(os.Stderr, nil))))
//
// In an effect function, slog.InfoContext("charged", "amount", n) is given
// the function's ctx, so its lines carry the function's span.
func LogHandler(h slog.Handler) slog.Handler { return logHandler{base: h, h: h} }

// logHandler adds the IDs at the top level of a record. Attributes added in
// Handle go into the groups opened with WithGroup, so with groups open it
// adds them to base, the handler before the first group, and replays what
// came after it.
type logHandler struct {
	base  slog.Handler // h before the first WithGroup
	h     slog.Handler
	after []func(slog.Handler) slog.Handler // WithGroup and later WithAttrs
}

func (l logHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return l.h.Enabled(ctx, level)
}

func (l logHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return l.h.Handle(ctx, r)
	}
	ids := []slog.Attr{slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String())}
	if len(l.after) == 0 {
		r.AddAttrs(ids...)
		return l.h.Handle(ctx, r)
	}
	h := l.base.WithAttrs(ids)
	for _, f := range l.after {
		h = f(h)
	}
	return h.Handle(ctx, r)
}

func (l logHandler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(l.after) == 0 {
		h := l.h.WithAttrs(as)
		return logHandler{base: h, h: h}
	}
	return l.then(func(h slog.Handler) slog.Handler { return h.WithAttrs(as) })
}

func (l logHandler) WithGroup(name string) slog.Handler {
	return l.then(func(h slog.Handler) slog.Handler { return h.WithGroup(name) })
}

func (l logHandler) then(f func(slog.Handler) slog.Handler) logHandler {
	return logHandler{base: l.base, h: f(l.h), after: append(slices.Clip(l.after), f)}
}
