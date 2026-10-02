package trace

import (
	"context"
	"errors"
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
