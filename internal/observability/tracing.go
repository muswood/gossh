// owner: muswood | Email: mumu920@outlook.com
package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Tracer uses the OpenTelemetry API, so applications can install an OTLP,
// Jaeger, or console provider without coupling Agent code to an exporter.
// The existing Recorder remains the local diagnostics fallback.
type Tracer struct {
	tracer   trace.Tracer
	recorder *Recorder
}

func NewTracer(recorder *Recorder) *Tracer {
	return &Tracer{tracer: otel.Tracer("gossh/agent"), recorder: recorder}
}

// SetProvider lets the host application install an OTLP, Jaeger, or console
// provider without making the core Agent package depend on an exporter.
func (t *Tracer) SetProvider(provider trace.TracerProvider) {
	if t == nil || provider == nil {
		return
	}
	t.tracer = provider.Tracer("gossh/agent")
}

func (t *Tracer) Start(ctx context.Context, name string, fields map[string]string) (context.Context, func(error)) {
	if t == nil {
		return ctx, func(error) {}
	}
	attrs := make([]attribute.KeyValue, 0, len(fields))
	for key, value := range fields {
		attrs = append(attrs, attribute.String(key, value))
	}
	started := time.Now()
	spanCtx, span := t.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	return spanCtx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
		if t.recorder != nil {
			status := "ok"
			if err != nil {
				status = "error"
			}
			recordFields := make(map[string]interface{}, len(fields))
			for key, value := range fields {
				recordFields[key] = value
			}
			t.recorder.Record("agent", name, status, started, recordFields)
		}
	}
}
