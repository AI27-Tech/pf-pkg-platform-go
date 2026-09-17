package outbox_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/AI27-Tech/pf-pkg-platform-go/internal/dbtest"
	"github.com/AI27-Tech/pf-pkg-platform-go/outbox"
)

// TestTrazaHTTPHastaOutbox verifica el criterio E0-T1 de punta a punta: el trazo
// de una petición HTTP se propaga hasta el evento de outbox. Un servidor
// instrumentado con otelhttp recibe una petición; el handler publica un evento
// en la outbox dentro de una transacción; luego se comprueba que el
// trace_context guardado en la fila tiene el MISMO trace_id que el span que
// otelhttp creó para esa petición.
//
// Se salta si PLATFORM_GO_TEST_DB no está definido.
func TestTrazaHTTPHastaOutbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Propagador y tracer provider globales, como en producción.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	defer func() { _ = tp.Shutdown(ctx) }()

	dsn := dbtest.DSN(t)
	suf := fmt.Sprintf("_t%d", time.Now().UnixNano())
	schema := "traza_test" + suf

	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`CREATE SCHEMA %q`, schema))
	defer dbtest.Exec(t, ctx, admin, fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf("SET search_path TO %q", schema))
	dbtest.Exec(t, ctx, admin, outbox.OutboxDDL)

	pool, err := pgxpool.New(ctx, withSearchPath(dsn, schema))
	if err != nil {
		t.Fatalf("crear pool: %v", err)
	}
	defer pool.Close()

	pub := outbox.NewPublisher()

	// El handler corre DENTRO del span que otelhttp crea para la petición.
	// Captura el trace_id que ve el servidor y publica el evento con r.Context().
	var traceIDVistoPorServidor trace.TraceID
	handler := func(w http.ResponseWriter, r *http.Request) {
		traceIDVistoPorServidor = trace.SpanContextFromContext(r.Context()).TraceID()

		tx, err := pool.Begin(r.Context())
		if err != nil {
			http.Error(w, "begin", http.StatusInternalServerError)
			return
		}
		ev := outbox.Event{Tipo: "viaje.creado", AgregadoID: "viaje-abc", Payload: map[string]any{"x": 1}}
		if err := pub.Publish(r.Context(), tx, ev); err != nil {
			_ = tx.Rollback(r.Context())
			http.Error(w, "publish", http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			http.Error(w, "commit", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}

	// Servidor instrumentado igual que server.New.
	srv := httptest.NewServer(otelhttp.NewHandler(http.HandlerFunc(handler), "http.server"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/viajes")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, se esperaba 204", resp.StatusCode)
	}
	if !traceIDVistoPorServidor.IsValid() {
		t.Fatal("el servidor no vio un trace_id válido; ¿otelhttp no instrumentó?")
	}

	// Leer el trace_context guardado en la fila de outbox y restaurar el span.
	var traceCtx map[string]string
	row := pool.QueryRow(ctx, fmt.Sprintf(`SELECT trace_context FROM %q.outbox WHERE tipo = $1`, schema), "viaje.creado")
	if err := row.Scan(&traceCtx); err != nil {
		t.Fatalf("leer trace_context: %v", err)
	}

	sc := pub.SpanContextFromRow(traceCtx)
	if !sc.IsValid() {
		t.Fatal("el trace_context guardado no restauró un SpanContext válido")
	}
	if sc.TraceID() != traceIDVistoPorServidor {
		t.Errorf("trace_id en outbox = %s, se esperaba el de la petición %s", sc.TraceID(), traceIDVistoPorServidor)
	}
}
