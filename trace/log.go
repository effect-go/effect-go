package trace

import (
	"context"
	"log/slog"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// LogHandler wraps h so that records logged with a context holding a span
// carry its trace_id and span_id, which link each log line to its trace:
//
//	slog.SetDefault(slog.New(trace.LogHandler(slog.NewJSONHandler(os.Stderr, nil))))
//
// In an effect function, slog.InfoContext("charged", "amount", n) is given
// the function's ctx, so its lines carry the function's span.
func LogHandler(h slog.Handler) slog.Handler { return logHandler{h} }

type logHandler struct{ slog.Handler }

func (h logHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := oteltrace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h logHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return logHandler{h.Handler.WithAttrs(as)}
}

func (h logHandler) WithGroup(name string) slog.Handler {
	return logHandler{h.Handler.WithGroup(name)}
}
