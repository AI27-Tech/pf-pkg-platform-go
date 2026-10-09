//go:build nats

// Prueba del emisor de heartbeat contra NATS real (principle-prove-it-works).
// Build tag "nats"; corre con el contenedor efímero.
package nats

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
)

// TestHeartbeatLlegaPorBroadcast verifica que el emisor difunde latidos
// periódicos por el broadcast, con el Tipo reservado, y que un suscriptor los
// recibe. Usa un intervalo corto para no esperar segundos.
func TestHeartbeatLlegaPorBroadcast(t *testing.T) {
	c, err := Conectar(urlNATS())
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	defer c.Cerrar()

	var mu sync.Mutex
	var tipos []string
	wSub, err := c.SubscribeBroadcast(func(_ context.Context, s bus.Sobre) error {
		mu.Lock()
		tipos = append(tipos, s.Tipo)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("suscribir: %v", err)
	}

	emisor := NuevoEmisorHeartbeat(c, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = wSub.Run(ctx) }()
	go func() { _ = emisor.Run(ctx) }()
	time.Sleep(100 * time.Millisecond) // deja que el suscriptor se registre

	// En ~250ms con intervalo de 50ms deberían caber al menos 2 latidos.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(tipos)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(tipos) < 2 {
		t.Fatalf("se esperaban >=2 latidos, llegaron %d", len(tipos))
	}
	for _, tp := range tipos {
		if tp != TipoHeartbeat {
			t.Errorf("latido con Tipo %q, se esperaba %q", tp, TipoHeartbeat)
		}
	}
}

// TestEmisorDrenaAlCancelar verifica que Run devuelve nil al cancelar el ctx
// (contrato de worker.Worker), sin colgarse.
func TestEmisorDrenaAlCancelar(t *testing.T) {
	c, err := Conectar(urlNATS())
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	defer c.Cerrar()

	emisor := NuevoEmisorHeartbeat(c, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	hecho := make(chan error, 1)
	go func() { hecho <- emisor.Run(ctx) }()
	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case err := <-hecho:
		if err != nil {
			t.Errorf("Run debió devolver nil al cancelar, devolvió %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run no drenó al cancelar el contexto")
	}
}
