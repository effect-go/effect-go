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

// A span whose function was interrupted isn't an error: it gets an event.
func TestInterruptedSpan(t *testing.T) {
	rec := record(t)
	func() {
		_, span := Start(t.Context(), "Race.Loser")
		err := fmt.Errorf("fetch: %w", context.Canceled)
		End(span, &err)
	}()
	s := rec.Ended()[0]
	if s.Status().Code != codes.Unset || len(s.Events()) != 1 || s.Events()[0].Name != "interrupted" {
		t.Fatalf("status %v, events %v", s.Status(), s.Events())
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
