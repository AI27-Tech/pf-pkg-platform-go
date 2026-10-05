package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Inject extrae el contexto de traza activo (traceparent/tracestate W3C) a un
// carrier de mapa serializable. Lo usan la outbox (al persistir la fila) y el
// publicador del bus. Si no hay span activo o nadie configuró el propagador, el
// carrier queda vacío, lo que no rompe nada.
func Inject(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier
}

// Extract restaura un contexto con el span padre a partir del carrier guardado.
// Lo usan el worker de outbox al publicar y el consumidor del bus al recibir,
// de modo que el trazo cuelgue del que originó el evento.
func Extract(ctx context.Context, carrier map[string]string) context.Context {
	if len(carrier) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(carrier))
}

// IniciarSpanConsumidor continúa el trazo guardado en el carrier y abre un span
// de consumo con el nombre dado. Devuelve el contexto con el span y el span, que
// el llamador debe cerrar (defer span.End()).
func IniciarSpanConsumidor(ctx context.Context, carrier map[string]string, nombre string) (context.Context, trace.Span) {
	ctx = Extract(ctx, carrier)
	return Tracer("bus.consumer").Start(ctx, nombre, trace.WithSpanKind(trace.SpanKindConsumer))
}
