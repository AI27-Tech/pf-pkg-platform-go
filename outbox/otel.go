package outbox

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// otelPropagator devuelve el propagador de contexto global de OpenTelemetry.
// observability.InitTracerProvider establece uno W3C (traceparent/tracestate);
// si nadie lo configuró, otel devuelve un propagador no-op y el carrier queda
// vacío, lo que no rompe la publicación.
func otelPropagator() propagation.TextMapPropagator {
	return otel.GetTextMapPropagator()
}
