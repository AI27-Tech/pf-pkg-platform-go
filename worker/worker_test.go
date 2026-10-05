package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/health"
)

// workerFalso es un Worker de prueba controlable.
type workerFalso struct {
	nombre   string
	corrio   atomic.Bool
	draining atomic.Bool
	readyErr error
	runErr   error
	// bloquearHasta, si no es nil, hace que Run bloquee hasta que ctx se cancele.
	bloquear bool
}

func (w *workerFalso) Name() string { return w.nombre }

func (w *workerFalso) Run(ctx context.Context) error {
	w.corrio.Store(true)
	if w.runErr != nil {
		return w.runErr
	}
	if w.bloquear {
		<-ctx.Done()
		w.draining.Store(true)
		return nil
	}
	return nil
}

func (w *workerFalso) Ready(ctx context.Context) error { return w.readyErr }

func TestRunGroup_ArrancaTodosYDrenaAlCancelar(t *testing.T) {
	a := &workerFalso{nombre: "a", bloquear: true}
	b := &workerFalso{nombre: "b", bloquear: true}
	reg := health.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunGroup(ctx, reg, a, b) }()

	// Dar tiempo a que arranquen.
	time.Sleep(50 * time.Millisecond)
	if !a.corrio.Load() || !b.corrio.Load() {
		t.Fatal("ambos workers deberían haber arrancado")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunGroup debería volver sin error al cancelar: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunGroup no volvió tras cancelar (no drenó)")
	}

	if !a.draining.Load() || !b.draining.Load() {
		t.Error("ambos workers deberían haber drenado al cancelar")
	}
}

func TestRunGroup_RegistraReadiness(t *testing.T) {
	a := &workerFalso{nombre: "relay", bloquear: true}
	reg := health.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = RunGroup(ctx, reg, a) }()
	time.Sleep(50 * time.Millisecond)

	// La readiness del worker debe quedar registrada con el prefijo worker:.
	reg.SetReady(true)
	// No hay API pública para listar checks; verificamos indirectamente que
	// registrar dos veces el mismo nombre entra en conflicto (panic de health).
	defer func() {
		if r := recover(); r == nil {
			t.Error("registrar un check duplicado debería hacer panic")
		}
	}()
	reg.RegisterCheck("worker:relay", a.Ready)
}

func TestRunGroup_PropagaErrorYApagaLosDemas(t *testing.T) {
	malo := &workerFalso{nombre: "malo", runErr: errors.New("explotó")}
	bueno := &workerFalso{nombre: "bueno", bloquear: true}
	reg := health.NewRegistry()

	err := RunGroup(context.Background(), reg, malo, bueno)
	if err == nil {
		t.Fatal("RunGroup debería devolver el error del worker que falló")
	}
	if !bueno.draining.Load() {
		t.Error("el worker sano debería haber drenado tras el fallo del otro")
	}
}

func TestRunGroup_NombreDuplicado(t *testing.T) {
	a := &workerFalso{nombre: "x"}
	b := &workerFalso{nombre: "x"}
	if err := RunGroup(context.Background(), health.NewRegistry(), a, b); err == nil {
		t.Fatal("nombres duplicados deberían dar error")
	}
}

func TestRunGroup_SinWorkers(t *testing.T) {
	if err := RunGroup(context.Background(), health.NewRegistry()); err != nil {
		t.Fatalf("sin workers no debería fallar: %v", err)
	}
}
