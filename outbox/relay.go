package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier es la superficie mínima del pool que el Relay necesita. La cumple
// *pgxpool.Pool (y por tanto el wrapper *pgxpool.Pool de platform-go). Se
// declara aquí para no acoplar outbox al wrapper de pool y facilitar las
// pruebas con un doble.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// RelayOpciones ajusta el drenado. Los valores de arranque son provisionales
// (ADR 29 §4): un despliegue real los toma de configuración.
type RelayOpciones struct {
	// Intervalo entre barridos cuando no hay pendientes (default 1s).
	Intervalo time.Duration
	// Lote es el máximo de filas por barrido (default 100).
	Lote int
}

func (o RelayOpciones) conDefaults() RelayOpciones {
	if o.Intervalo <= 0 {
		o.Intervalo = time.Second
	}
	if o.Lote <= 0 {
		o.Lote = 100
	}
	return o
}

// Relay es el worker (ADR 29 §6) que drena la tabla outbox del módulo y publica
// cada fila por el bus.Publisher. Marca la fila como publicada SOLO tras el
// acuse del transporte (Publish sin error): si el bus falla, la fila sigue
// pendiente y se reintenta en el siguiente barrido (at-least-once). El orden por
// clave de agregado lo garantiza el bus a partir de AgregadoID.
//
// Es agnóstico al transporte: recibe un bus.Publisher. En desarrollo es
// bus/memory; en producción, el adaptador del broker (en espera de ADR 32).
type Relay struct {
	nombre     string
	pool       Querier
	pub        bus.Publisher
	opts       RelayOpciones
	propagator *Publisher // reutiliza la lógica de traza del publicador
}

// NewRelay crea el worker de relay para un módulo. El nombre debe ser estable
// (etiqueta de métricas y logs); por convención, "outbox-relay:<modulo>".
func NewRelay(nombre string, pool Querier, pub bus.Publisher, opts RelayOpciones) *Relay {
	return &Relay{
		nombre:     nombre,
		pool:       pool,
		pub:        pub,
		opts:       opts.conDefaults(),
		propagator: NewPublisher(),
	}
}

// Name implementa worker.Worker.
func (r *Relay) Name() string { return r.nombre }

// Ready implementa worker.Worker: el relay está listo si el pool responde a una
// consulta trivial. Se mantiene barato para no castigar el readiness.
func (r *Relay) Ready(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT 1`)
	if err != nil {
		return fmt.Errorf("%s: pool no disponible: %w", r.nombre, err)
	}
	rows.Close()
	return rows.Err()
}

// Run drena en bucle hasta que ctx se cancele. Al cancelar, deja las filas no
// publicadas tal cual (no acusa trabajo no terminado): se publicarán al
// rearrancar.
func (r *Relay) Run(ctx context.Context) error {
	for {
		n, err := r.barrer(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil && n == r.opts.Lote {
			// El lote vino lleno: puede haber más, barre de inmediato.
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(r.opts.Intervalo):
		}
	}
}

// fila es una fila pendiente de la outbox.
type fila struct {
	id           int64
	eventoID     string
	tipo         string
	version      int
	agregadoID   string
	sensibilidad string
	ocurridoEn   time.Time
	payload      []byte
	traceCtx     map[string]string
}

// barrer lee hasta Lote filas pendientes (más viejas primero) y publica cada
// una. Devuelve cuántas publicó con éxito.
func (r *Relay) barrer(ctx context.Context) (int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, evento_id, tipo, version, agregado_id, sensibilidad, ocurrido_en, payload, trace_context
		   FROM outbox
		  WHERE publicado = false
		  ORDER BY creado_en
		  LIMIT $1`, r.opts.Lote)
	if err != nil {
		return 0, fmt.Errorf("%s: leer pendientes: %w", r.nombre, err)
	}

	var pendientes []fila
	for rows.Next() {
		var f fila
		var traceBytes []byte
		if err := rows.Scan(&f.id, &f.eventoID, &f.tipo, &f.version, &f.agregadoID,
			&f.sensibilidad, &f.ocurridoEn, &f.payload, &traceBytes); err != nil {
			rows.Close()
			return 0, fmt.Errorf("%s: escanear fila: %w", r.nombre, err)
		}
		if len(traceBytes) > 0 {
			_ = json.Unmarshal(traceBytes, &f.traceCtx)
		}
		pendientes = append(pendientes, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("%s: iterar pendientes: %w", r.nombre, err)
	}

	publicadas := 0
	for _, f := range pendientes {
		if ctx.Err() != nil {
			return publicadas, nil
		}
		// Continuar el trazo que originó el evento.
		pubCtx := r.propagator.TraceContextFromRow(ctx, f.traceCtx)
		s := bus.Sobre{
			EventoID:     f.eventoID,
			Tipo:         f.tipo,
			Version:      f.version,
			AgregadoID:   f.agregadoID,
			Sensibilidad: bus.Sensibilidad(f.sensibilidad),
			OcurridoEn:   f.ocurridoEn,
			Payload:      f.payload,
			TraceContext: f.traceCtx,
		}
		if err := r.pub.Publish(pubCtx, s); err != nil {
			// No se acusó: la fila sigue pendiente y se reintenta. Detenemos el
			// barrido para no romper el orden por agregado de este lote.
			return publicadas, nil
		}
		if _, err := r.pool.Exec(ctx,
			`UPDATE outbox SET publicado = true, publicado_en = now() WHERE id = $1`, f.id); err != nil {
			return publicadas, fmt.Errorf("%s: marcar publicado: %w", r.nombre, err)
		}
		publicadas++
	}
	return publicadas, nil
}
