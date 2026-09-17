package outbox_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/AI27-Tech/pf-pkg-platform-go/outbox"
)

// TestTraceRoundTrip verifica, sin base de datos, que el Publisher inyecta el
// contexto de traza activo y que TraceContextFromRow lo restaura con el mismo
// trace_id (base de la propagación HTTP -> outbox del criterio E0-T1).
func TestTraceRoundTrip(t *testing.T) {
	// Propagador W3C global (lo que hace observability.InitTracerProvider).
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Un span real del que derivar un SpanContext válido.
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	ctx, span := tp.Tracer("test").Start(context.Background(), "http.request")
	defer span.End()

	wantTraceID := span.SpanContext().TraceID()

	pub := outbox.NewPublisher()

	// Inyectar desde el ctx con span y restaurar; debe conservar el trace_id.
	carrier := pub.InjectForTest(ctx)
	if len(carrier) == 0 {
		t.Fatal("el carrier no debería estar vacío con un span activo")
	}

	sc := pub.SpanContextFromRow(carrier)
	if !sc.IsValid() {
		t.Fatal("el SpanContext restaurado debería ser válido")
	}
	if sc.TraceID() != wantTraceID {
		t.Errorf("trace_id restaurado = %s, se esperaba %s", sc.TraceID(), wantTraceID)
	}
}

// TestTraceSinSpan verifica que sin span activo el carrier queda vacío y no rompe.
func TestTraceSinSpan(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	pub := outbox.NewPublisher()

	carrier := pub.InjectForTest(context.Background())
	sc := pub.SpanContextFromRow(carrier)
	if sc.IsValid() {
		t.Error("sin span activo, el SpanContext restaurado no debería ser válido")
	}
}

var _ = trace.SpanContext{} // mantiene el import de trace explícito para el lector
