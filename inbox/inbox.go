// Package inbox ofrece la deduplicación idempotente de eventos consumidos
// (ADR 29 §1). El consumidor registra el EventoID en la tabla inbox_evento
// dentro de la MISMA transacción que aplica el efecto de dominio: si el evento
// ya se procesó, el alta falla por la restricción de unicidad y el consumidor
// omite el efecto. Así "duplicado → un solo efecto" se cumple sin lógica de
// negocio en platform-go, que no administra schemas: cada módulo crea la tabla
// en su propio schema con InboxDDL.
package inbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrYaProcesado indica que el EventoID ya estaba registrado: el consumidor
// debe omitir el efecto y acusar el sobre (es un duplicado, no un fallo).
var ErrYaProcesado = errors.New("inbox: evento ya procesado")

// InboxDDL es el DDL de referencia de la tabla de deduplicación. Cada módulo lo
// aplica en su schema mediante sus migraciones.
const InboxDDL = `
CREATE TABLE IF NOT EXISTS inbox_evento (
  evento_id    UUID        PRIMARY KEY,
  tipo         TEXT        NOT NULL,
  consumidor   TEXT        NOT NULL,
  procesado_en TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

// Registrar da de alta el EventoID para el consumidor dado dentro de la
// transacción tx. Devuelve ErrYaProcesado si el evento ya estaba registrado
// (violación de unicidad), de modo que el consumidor distinga un duplicado de
// un fallo real. Debe llamarse en la misma tx que aplica el efecto de dominio.
func Registrar(ctx context.Context, tx pgx.Tx, eventoID, tipo, consumidor string) error {
	if eventoID == "" {
		return fmt.Errorf("inbox: evento_id no debe estar vacío")
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO inbox_evento (evento_id, tipo, consumidor) VALUES ($1, $2, $3)`,
		eventoID, tipo, consumidor,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return ErrYaProcesado
		}
		return fmt.Errorf("inbox: registrar evento %s: %w", eventoID, err)
	}
	return nil
}
