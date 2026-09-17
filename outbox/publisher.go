// Package outbox ofrece el publicador transaccional de eventos.
// Inserta en la tabla outbox del schema del llamador, dentro de la misma
// transacción que el cambio de dominio. No decide cuándo publicar (eso
// es del caso de uso). El worker que drena la outbox es responsabilidad
// de cada cmd/ de composición.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Publisher inserta eventos en la tabla outbox dentro de una transacción existente.
type Publisher struct{}

// NewPublisher crea un nuevo publicador de outbox.
func NewPublisher() *Publisher {
	return &Publisher{}
}

// Event representa un solo evento a publicar vía la outbox.
type Event struct {
	// Topic es el destino lógico (ej. "viaje.estado_cambiado").
	Topic string
	// Payload es el cuerpo del evento, típicamente un evento de dominio serializado como JSON.
	Payload any
	// EventType es el nombre del tipo de evento (ej. "viaje.estado_cambiado").
	EventType string
	// EventVersion es la versión del esquema del payload del evento.
	EventVersion int
}

// Publish inserta uno o más eventos en la tabla outbox dentro de la transacción dada.
// La tabla outbox debe existir en el schema del llamador con la estructura estándar:
//
//	CREATE TABLE outbox (
//	  id          BIGSERIAL PRIMARY KEY,
//	  topic       TEXT NOT NULL,
//	  event_type  TEXT NOT NULL,
//	  event_version INT NOT NULL DEFAULT 1,
//	  payload     JSONB NOT NULL,
//	  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
//	  published   BOOLEAN NOT NULL DEFAULT false
//	);
//
// El llamador es responsable de hacer commit de la transacción.
func (p *Publisher) Publish(ctx context.Context, tx pgx.Tx, events ...Event) error {
	for i, ev := range events {
		if ev.Topic == "" {
			return fmt.Errorf("outbox: evento %d: el topic no debe estar vacío", i)
		}
		if ev.EventType == "" {
			return fmt.Errorf("outbox: evento %d: el event_type no debe estar vacío", i)
		}

		payloadBytes, err := json.Marshal(ev.Payload)
		if err != nil {
			return fmt.Errorf("outbox: evento %d: serializar payload: %w", i, err)
		}

		version := ev.EventVersion
		if version == 0 {
			version = 1
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO outbox (topic, event_type, event_version, payload, created_at, published)
			 VALUES ($1, $2, $3, $4, $5, false)`,
			ev.Topic, ev.EventType, version, payloadBytes, time.Now(),
		)
		if err != nil {
			return fmt.Errorf("outbox: evento %d: insertar: %w", i, err)
		}
	}
	return nil
}

// PublishWithMetadata inserta eventos con campos de metadata adicionales.
func (p *Publisher) PublishWithMetadata(ctx context.Context, tx pgx.Tx, events ...EventWithMeta) error {
	for i, ev := range events {
		if ev.Topic == "" {
			return fmt.Errorf("outbox: evento %d: el topic no debe estar vacío", i)
		}
		if ev.EventType == "" {
			return fmt.Errorf("outbox: evento %d: el event_type no debe estar vacío", i)
		}

		payloadBytes, err := json.Marshal(ev.Payload)
		if err != nil {
			return fmt.Errorf("outbox: evento %d: serializar payload: %w", i, err)
		}

		version := ev.EventVersion
		if version == 0 {
			version = 1
		}

		metadataBytes, err := json.Marshal(ev.Metadata)
		if err != nil {
			return fmt.Errorf("outbox: evento %d: serializar metadata: %w", i, err)
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO outbox (topic, event_type, event_version, payload, metadata, created_at, published)
			 VALUES ($1, $2, $3, $4, $5, $6, false)`,
			ev.Topic, ev.EventType, version, payloadBytes, metadataBytes, time.Now(),
		)
		if err != nil {
			return fmt.Errorf("outbox: evento %d: insertar: %w", i, err)
		}
	}
	return nil
}

// EventWithMeta es un Event que lleva metadata adicional.
type EventWithMeta struct {
	Event
	// Metadata son pares clave-valor adicionales almacenados junto con el evento.
	Metadata map[string]string
}
