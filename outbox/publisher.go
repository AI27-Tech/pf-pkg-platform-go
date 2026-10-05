// Package outbox ofrece el publicador transaccional de eventos.
// Inserta en la tabla outbox del schema del llamador, dentro de la misma
// transacción que el cambio de dominio. No decide cuándo publicar (eso es del
// caso de uso). El worker que drena la outbox es responsabilidad de cada cmd/
// de composición (E0-T10, ADR 29).
//
// La fila de outbox lleva la capa de DOMINIO del sobre de eventos —los mismos
// campos que `Sobre` en pf-pkg-contracts (contracts/events): evento_id, tipo,
// version, agregado_id, ocurrido_en, sensibilidad—. La capa de transporte
// (stream_id/epoch/seq de realtime) NO vive aquí (ADR 28). El Relay (ver
// relay.go) publica por bus.Publisher y, tras el acuse del transporte, marca la
// fila como publicada (ADR 29).
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Sensibilidad clasifica el evento en la superficie amplia o restringida del
// bus (ADR 29). Coincide con el enum `Sensibilidad` del contrato. Los eventos
// de viaje del tracer son AMPLIA; conversación/bandeja/emergencia, RESTRINGIDA.
type Sensibilidad string

const (
	SensibilidadAmplia      Sensibilidad = "amplia"
	SensibilidadRestringida Sensibilidad = "restringida"
)

// Event es la capa de dominio del sobre de un evento a publicar por la outbox.
// Refleja `Sobre` de contracts/events (E0-T2).
type Event struct {
	// EventoID es el identificador único del evento y la clave de idempotencia
	// del consumidor (inbox_evento). Si se deja vacío, Publish genera un UUID.
	EventoID string
	// Tipo es el nombre del tipo de evento, p. ej. "viaje.estado_cambiado".
	// Debe estar en el registro versionado de tipos permitidos del contrato.
	Tipo string
	// Version es la versión del contrato del payload de este tipo.
	// Si es 0, se registra como 1.
	Version int
	// AgregadoID es la identidad canónica del agregado del que deriva el orden
	// (p. ej. el viaje_id para la familia viaje.*). De aquí el bus deriva la
	// clave de orden; no existe una clave global (ADR 29).
	AgregadoID string
	// Sensibilidad indica la superficie del bus. Por defecto (vacío) es amplia.
	Sensibilidad Sensibilidad
	// OcurridoEn es el momento en que el hecho ocurrió (tiempo de dominio, UTC).
	// Si es cero, Publish usa la hora actual.
	OcurridoEn time.Time
	// Payload es el cuerpo del evento (evento de dominio serializado como JSON).
	Payload any
}

// Publisher inserta eventos en la tabla outbox dentro de una transacción existente.
type Publisher struct {
	propagator propagation.TextMapPropagator
}

// NewPublisher crea un publicador de outbox. Usa el propagador global de
// OpenTelemetry para persistir el contexto de traza en cada fila, de modo que
// el trazo de la petición HTTP se propague hasta el evento (E0-T1 / E0-T10).
func NewPublisher() *Publisher {
	return &Publisher{propagator: otelPropagator()}
}

// Publish inserta uno o más eventos en la tabla outbox dentro de la transacción
// dada. La tabla outbox debe existir en el schema del llamador con la estructura
// estándar de OutboxDDL. El llamador es responsable de hacer commit de la
// transacción: el evento y el cambio de dominio se comprometen juntos (P17).
func (p *Publisher) Publish(ctx context.Context, tx pgx.Tx, events ...Event) error {
	for i := range events {
		ev := &events[i]
		if ev.Tipo == "" {
			return fmt.Errorf("outbox: evento %d: el tipo no debe estar vacío", i)
		}
		if ev.AgregadoID == "" {
			return fmt.Errorf("outbox: evento %d: el agregado_id no debe estar vacío (clave de orden)", i)
		}
		if ev.EventoID == "" {
			ev.EventoID = uuid.NewString()
		}
		if ev.Version == 0 {
			ev.Version = 1
		}
		if ev.Sensibilidad == "" {
			ev.Sensibilidad = SensibilidadAmplia
		}
		if ev.OcurridoEn.IsZero() {
			ev.OcurridoEn = time.Now().UTC()
		}

		payloadBytes, err := json.Marshal(ev.Payload)
		if err != nil {
			return fmt.Errorf("outbox: evento %d: serializar payload: %w", i, err)
		}

		// Persistir el contexto de traza (traceparent/tracestate W3C) para que
		// el worker lo restaure al publicar y el trazo continúe HTTP -> outbox
		// -> bus. Si no hay span activo, el carrier queda vacío.
		traceCarrier := p.injectTrace(ctx)
		traceBytes, err := json.Marshal(traceCarrier)
		if err != nil {
			return fmt.Errorf("outbox: evento %d: serializar traza: %w", i, err)
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO outbox
			   (evento_id, tipo, version, agregado_id, sensibilidad, ocurrido_en, payload, trace_context, publicado)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false)`,
			ev.EventoID, ev.Tipo, ev.Version, ev.AgregadoID, string(ev.Sensibilidad),
			ev.OcurridoEn, payloadBytes, traceBytes,
		)
		if err != nil {
			return fmt.Errorf("outbox: evento %d (%s): insertar: %w", i, ev.Tipo, err)
		}
	}
	return nil
}

// injectTrace extrae el contexto de traza del ctx a un carrier serializable.
func (p *Publisher) injectTrace(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	if p.propagator != nil {
		p.propagator.Inject(ctx, carrier)
	}
	return carrier
}

// InjectForTest expone injectTrace para pruebas del round-trip de propagación.
func (p *Publisher) InjectForTest(ctx context.Context) map[string]string {
	return p.injectTrace(ctx)
}

// TraceContextFromRow restaura un contexto con el span padre a partir del
// carrier de traza guardado en la fila de outbox. Lo usa el worker al publicar
// para que el trazo del bus cuelgue del trazo que originó el evento.
func (p *Publisher) TraceContextFromRow(ctx context.Context, traceContext map[string]string) context.Context {
	if p.propagator == nil || len(traceContext) == 0 {
		return ctx
	}
	return p.propagator.Extract(ctx, propagation.MapCarrier(traceContext))
}

// SpanContextFromRow es una ayuda para pruebas y diagnóstico: devuelve el
// SpanContext restaurado desde el carrier guardado.
func (p *Publisher) SpanContextFromRow(traceContext map[string]string) trace.SpanContext {
	ctx := p.TraceContextFromRow(context.Background(), traceContext)
	return trace.SpanContextFromContext(ctx)
}

// OutboxDDL es el DDL de referencia de la tabla outbox que cada módulo crea en
// su propio schema mediante sus migraciones (platform-go no administra schemas).
// Refleja la capa de dominio del sobre de contracts/events.
const OutboxDDL = `
CREATE TABLE IF NOT EXISTS outbox (
  id            BIGSERIAL PRIMARY KEY,
  evento_id     UUID        NOT NULL UNIQUE,
  tipo          TEXT        NOT NULL,
  version       INT         NOT NULL DEFAULT 1,
  agregado_id   TEXT        NOT NULL,
  sensibilidad  TEXT        NOT NULL DEFAULT 'amplia',
  ocurrido_en   TIMESTAMPTZ NOT NULL,
  payload       JSONB       NOT NULL,
  trace_context JSONB       NOT NULL DEFAULT '{}'::jsonb,
  publicado     BOOLEAN     NOT NULL DEFAULT false,
  publicado_en  TIMESTAMPTZ,
  creado_en     TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- El Relay drena por creado_en las filas no publicadas; el orden por agregado
-- lo impone el bus a partir de agregado_id (ADR 29).
CREATE INDEX IF NOT EXISTS outbox_pendientes_idx
  ON outbox (creado_en) WHERE publicado = false;
`
