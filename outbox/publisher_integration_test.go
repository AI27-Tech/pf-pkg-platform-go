package outbox_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AI27-Tech/pf-pkg-platform-go/internal/dbtest"
	"github.com/AI27-Tech/pf-pkg-platform-go/outbox"
)

// TestOutboxSobreviveReinicio verifica el criterio E0-T1: un evento publicado
// en la outbox sobrevive un reinicio del proceso antes de entregarse al broker
// (queda en la tabla, no publicado, para que el worker lo retome).
//
// Se salta si PLATFORM_GO_TEST_DB no está definido.
func TestOutboxSobreviveReinicio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn := dbtest.DSN(t)
	suf := fmt.Sprintf("_t%d", time.Now().UnixNano())
	schema := "outbox_test" + suf

	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()

	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`CREATE SCHEMA %q`, schema))
	defer dbtest.Exec(t, ctx, admin, fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema))

	// Crear la tabla outbox del módulo dentro de su schema, con el DDL de referencia.
	dbtest.Exec(t, ctx, admin, fmt.Sprintf("SET search_path TO %q", schema))
	dbtest.Exec(t, ctx, admin, outbox.OutboxDDL)

	// --- "Proceso 1": publica un evento dentro de una transacción y hace commit. ---
	var eventoID string
	func() {
		pool, err := pgxpool.New(ctx, withSearchPath(dsn, schema))
		if err != nil {
			t.Fatalf("proceso 1: crear pool: %v", err)
		}
		defer pool.Close()

		pub := outbox.NewPublisher()

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("proceso 1: begin: %v", err)
		}
		ev := outbox.Event{
			Tipo:       "viaje.creado",
			AgregadoID: "viaje-123",
			Payload:    map[string]any{"origen": "CDMX", "destino": "GDL"},
		}
		if err := pub.Publish(ctx, tx, ev); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("proceso 1: publish: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("proceso 1: commit: %v", err)
		}

		// Recuperar el evento_id generado para verificarlo tras el "reinicio".
		row := pool.QueryRow(ctx,
			fmt.Sprintf(`SELECT evento_id::text FROM %q.outbox WHERE tipo = $1`, schema),
			"viaje.creado")
		if err := row.Scan(&eventoID); err != nil {
			t.Fatalf("proceso 1: leer evento_id: %v", err)
		}
	}()
	// Aquí el "proceso 1" ya terminó: su pool está cerrado. El evento sólo
	// existe si se persistió de forma durable, no en memoria del proceso.

	// --- "Proceso 2" (reinicio): un pool y worker nuevos ven el evento pendiente. ---
	pool2, err := pgxpool.New(ctx, withSearchPath(dsn, schema))
	if err != nil {
		t.Fatalf("proceso 2: crear pool: %v", err)
	}
	defer pool2.Close()

	var (
		gotID        string
		gotTipo      string
		gotAgregado  string
		gotPublicado bool
		gotVersion   int
		gotSensib    string
	)
	row := pool2.QueryRow(ctx,
		fmt.Sprintf(`SELECT evento_id::text, tipo, agregado_id, publicado, version, sensibilidad
		             FROM %q.outbox WHERE evento_id = $1`, schema), eventoID)
	if err := row.Scan(&gotID, &gotTipo, &gotAgregado, &gotPublicado, &gotVersion, &gotSensib); err != nil {
		t.Fatalf("proceso 2: el evento no sobrevivió el reinicio: %v", err)
	}

	// El evento sigue ahí, sin publicar, listo para que el worker lo retome.
	if gotID != eventoID {
		t.Errorf("evento_id = %q, se esperaba %q", gotID, eventoID)
	}
	if gotTipo != "viaje.creado" {
		t.Errorf("tipo = %q, se esperaba viaje.creado", gotTipo)
	}
	if gotAgregado != "viaje-123" {
		t.Errorf("agregado_id = %q, se esperaba viaje-123", gotAgregado)
	}
	if gotPublicado {
		t.Errorf("el evento no debería estar publicado tras el reinicio")
	}
	if gotVersion != 1 {
		t.Errorf("version = %d, se esperaba 1 (default)", gotVersion)
	}
	if gotSensib != string(outbox.SensibilidadAmplia) {
		t.Errorf("sensibilidad = %q, se esperaba amplia (default)", gotSensib)
	}
}

// withSearchPath agrega el search_path al DSN para que el pool opere dentro del
// schema de prueba sin cualificar cada tabla.
func withSearchPath(dsn, schema string) string {
	sep := "?"
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == '?' {
			sep = "&"
			break
		}
	}
	return dsn + sep + "search_path=" + schema
}
