package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- Doble de Querier en memoria ---

type filaFalsa struct {
	id        int64
	eventoID  string
	tipo      string
	publicado bool
}

type querierFalso struct {
	mu    sync.Mutex
	filas []*filaFalsa
}

func (q *querierFalso) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	// SELECT 1 (Ready)
	if len(args) == 0 {
		return &rowsFalsas{modo: "select1"}, nil
	}
	// SELECT pendientes
	var pend []*filaFalsa
	for _, f := range q.filas {
		if !f.publicado {
			pend = append(pend, f)
		}
	}
	return &rowsFalsas{modo: "pendientes", filas: pend}, nil
}

func (q *querierFalso) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	// UPDATE ... WHERE id = $1
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(args) == 1 {
		id, _ := args[0].(int64)
		for _, f := range q.filas {
			if f.id == id {
				f.publicado = true
			}
		}
	}
	return pgconn.CommandTag{}, nil
}

func (q *querierFalso) pendientes() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, f := range q.filas {
		if !f.publicado {
			n++
		}
	}
	return n
}

// rowsFalsas implementa pgx.Rows para el doble.
type rowsFalsas struct {
	modo  string
	filas []*filaFalsa
	idx   int
	done1 bool
}

func (r *rowsFalsas) Next() bool {
	if r.modo == "select1" {
		if r.done1 {
			return false
		}
		r.done1 = true
		return true
	}
	return r.idx < len(r.filas)
}

func (r *rowsFalsas) Scan(dest ...any) error {
	if r.modo == "select1" {
		return nil
	}
	f := r.filas[r.idx]
	r.idx++
	// id, evento_id, tipo, version, agregado_id, sensibilidad, ocurrido_en, payload, trace_context
	*dest[0].(*int64) = f.id
	*dest[1].(*string) = f.eventoID
	*dest[2].(*string) = f.tipo
	*dest[3].(*int) = 1
	*dest[4].(*string) = "AGG1"
	*dest[5].(*string) = string(bus.SensibilidadAmplia)
	*dest[6].(*time.Time) = time.Now().UTC()
	*dest[7].(*[]byte) = []byte(`{}`)
	*dest[8].(*[]byte) = []byte(`{}`)
	return nil
}

func (r *rowsFalsas) Close()                                       {}
func (r *rowsFalsas) Err() error                                   { return nil }
func (r *rowsFalsas) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *rowsFalsas) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *rowsFalsas) Values() ([]any, error)                       { return nil, nil }
func (r *rowsFalsas) RawValues() [][]byte                          { return nil }
func (r *rowsFalsas) Conn() *pgx.Conn                              { return nil }
func (r *rowsFalsas) TypeMap() *pgtype.Map                         { return nil }

// --- Publisher de prueba ---

type pubFalso struct {
	mu        sync.Mutex
	recibidos []bus.Sobre
	fallar    bool
}

func (p *pubFalso) Publish(ctx context.Context, s bus.Sobre) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fallar {
		return errors.New("transporte caído")
	}
	p.recibidos = append(p.recibidos, s)
	return nil
}

func (p *pubFalso) cuenta() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.recibidos)
}

// --- Pruebas ---

func TestRelay_PublicaYMarca(t *testing.T) {
	q := &querierFalso{filas: []*filaFalsa{
		{id: 1, eventoID: "e1", tipo: "viaje.creado"},
		{id: 2, eventoID: "e2", tipo: "viaje.creado"},
	}}
	pub := &pubFalso{}
	r := NewRelay("outbox-relay:test", q, pub, RelayOpciones{Intervalo: time.Millisecond})

	n, err := r.barrer(context.Background())
	if err != nil {
		t.Fatalf("barrer: %v", err)
	}
	if n != 2 {
		t.Fatalf("publicadas = %d, se esperaban 2", n)
	}
	if pub.cuenta() != 2 {
		t.Errorf("el publisher recibió %d, se esperaban 2", pub.cuenta())
	}
	if q.pendientes() != 0 {
		t.Errorf("quedaron %d pendientes, se esperaban 0", q.pendientes())
	}
}

func TestRelay_FalloDePublishDejaPendiente(t *testing.T) {
	q := &querierFalso{filas: []*filaFalsa{{id: 1, eventoID: "e1", tipo: "viaje.creado"}}}
	pub := &pubFalso{fallar: true}
	r := NewRelay("outbox-relay:test", q, pub, RelayOpciones{})

	n, err := r.barrer(context.Background())
	if err != nil {
		t.Fatalf("barrer no debería devolver error ante fallo de transporte: %v", err)
	}
	if n != 0 {
		t.Errorf("publicadas = %d, se esperaban 0 (transporte caído)", n)
	}
	if q.pendientes() != 1 {
		t.Errorf("la fila debió seguir pendiente, pendientes = %d", q.pendientes())
	}
}

func TestRelay_Ready(t *testing.T) {
	q := &querierFalso{}
	r := NewRelay("outbox-relay:test", q, &pubFalso{}, RelayOpciones{})
	if err := r.Ready(context.Background()); err != nil {
		t.Errorf("Ready debería pasar con pool sano: %v", err)
	}
}

func TestRelay_RunDrenaYPara(t *testing.T) {
	q := &querierFalso{filas: []*filaFalsa{{id: 1, eventoID: "e1", tipo: "viaje.creado"}}}
	pub := &pubFalso{}
	r := NewRelay("outbox-relay:test", q, pub, RelayOpciones{Intervalo: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	// Esperar a que publique.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && pub.cuenta() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run debería volver sin error al cancelar: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run no volvió al cancelar")
	}
	if pub.cuenta() != 1 {
		t.Errorf("se esperaba 1 publicación, hubo %d", pub.cuenta())
	}
}
