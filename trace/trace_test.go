package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/effect-go/effect-go/scope"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func record(t *testing.T) *tracetest.SpanRecorder {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

var errDown = errors.New("db down")

func load(ctx context.Context, fail bool) (err error) {
	ctx, span := Start(ctx, "Repo.Load")
	defer End(span, &err)
	if fail {
		return errDown
	}
	return nil
}

func explode(ctx context.Context) (err error) {
	_, span := Start(ctx, "Repo.Explode")
	defer End(span, &err)
	panic("boom")
}

func TestSpans(t *testing.T) {
	rec := record(t)
	load(t.Context(), false)
	load(t.Context(), true)
	func() {
		defer func() {
			if recover() != "boom" {
				t.Fatal("End must let the panic continue")
			}
		}()
		explode(t.Context())
	}()

	spans := rec.Ended()
	if len(spans) != 3 {
		t.Fatalf("%d spans", len(spans))
	}
	want := []struct {
		name string
		code codes.Code
	}{{"Repo.Load", codes.Unset}, {"Repo.Load", codes.Error}, {"Repo.Explode", codes.Error}}
	for i, w := range want {
		if spans[i].Name() != w.name || spans[i].Status().Code != w.code {
			t.Errorf("span %d: %s %v, want %s %v", i, spans[i].Name(), spans[i].Status().Code, w.name, w.code)
		}
	}
	if len(spans[1].Events()) != 1 || spans[1].Status().Description != "db down" {
		t.Errorf("error not recorded: %+v", spans[1].Status())
	}
}

func TestParentProvider(t *testing.T) {
	global := record(t)
	own := tracetest.NewSpanRecorder()
	ctx, root := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(own)).Tracer("app").Start(t.Context(), "request")
	load(ctx, false)
	root.End()
	load(t.Context(), false)
	if len(own.Ended()) != 2 || len(global.Ended()) != 1 {
		t.Errorf("parent's provider: %d spans, global: %d; want 2 and 1", len(own.Ended()), len(global.Ended()))
	}
}

func TestLogHandler(t *testing.T) {
	record(t)
	var buf bytes.Buffer
	log := slog.New(LogHandler(slog.NewJSONHandler(&buf, nil))).With("app", "shop")
	ctx, span := Start(t.Context(), "Shop.Checkout")
	log.InfoContext(ctx, "charged")
	span.End()
	log.InfoContext(t.Context(), "no span")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	want := `"trace_id":"` + span.SpanContext().TraceID().String() + `","span_id":"` + span.SpanContext().SpanID().String() + `"`
	if len(lines) != 2 || !strings.Contains(lines[0], want) || !strings.Contains(lines[0], `"app":"shop"`) || strings.Contains(lines[1], "trace_id") {
		t.Fatalf("logged:\n%s\nwant %s on the first line only", buf.String(), want)
	}
}

func panicsInTask(context.Context) (int, error) { panic("boom") }

// A task's panic comes back as a *scope.Panic: the span records its value
// and the task's stack.
func TestTaskPanic(t *testing.T) {
	rec := record(t)
	func() {
		defer func() { recover() }()
		var err error
		ctx, span := Start(t.Context(), "Load")
		defer End(span, &err)
		_, err = scope.All(ctx, panicsInTask)
	}()
	s, stack := rec.Ended()[0], ""
	for _, a := range s.Events()[0].Attributes {
		if a.Key == "exception.stacktrace" {
			stack = a.Value.AsString()
		}
	}
	if s.Status().Description != "panic: boom" || !strings.Contains(stack, "panicsInTask") {
		t.Fatalf("status %q, stack:\n%s", s.Status().Description, stack)
	}
}

// A span whose function was interrupted isn't an error: it gets an event.
// One that also failed, as All's when a task fails while being cancelled,
// is.
func TestInterruptedSpan(t *testing.T) {
	rec := record(t)
	for _, err := range []error{fmt.Errorf("fetch: %w", context.Canceled), errors.Join(context.Canceled, errDown)} {
		_, span := Start(t.Context(), "Race.Loser")
		End(span, &err)
	}
	s := rec.Ended()[0]
	if s.Status().Code != codes.Unset || len(s.Events()) != 1 || s.Events()[0].Name != "interrupted" {
		t.Fatalf("status %v, events %v", s.Status(), s.Events())
	}
	if s := rec.Ended()[1]; s.Status().Code != codes.Error {
		t.Fatalf("joined failure: status %v", s.Status())
	}
}

// The IDs stay at the top level of a record logged inside a group.
func TestLogHandlerGroup(t *testing.T) {
	record(t)
	var buf bytes.Buffer
	log := slog.New(LogHandler(slog.NewJSONHandler(&buf, nil))).With("app", "shop").WithGroup("req").With("id", 1)
	ctx, span := Start(t.Context(), "Shop.Checkout")
	defer span.End()
	log.InfoContext(ctx, "charged", "amount", 3)
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	req, _ := line["req"].(map[string]any)
	if line["trace_id"] != span.SpanContext().TraceID().String() || line["app"] != "shop" || req["id"] != 1.0 || req["amount"] != 3.0 {
		t.Fatalf("logged %s", buf.Bytes())
	}
}
