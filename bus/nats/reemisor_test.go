//go:build nats

// Prueba del reemisor contra NATS JetStream REAL (principle-prove-it-works).
// Build tag "nats" para no romper CI sin broker.
//
//	docker run --rm -p 4222:4222 -p 8222:8222 nats:2-alpine -js -m 8222
//	go test -tags nats -run TestReemisor ./bus/nats/
package nats

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
)

// TestReemisorDurableADifusion verifica el tramo del reemisor: un evento
// publicado al stream durable sale por el broadcast a un suscriptor de difusión,
// con sus campos intactos (ADR 29 §3).
func TestReemisorDurableADifusion(t *testing.T) {
	c, err := Conectar(urlNATS())
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	defer c.Cerrar()
	purgarStreams(t, c)

	reg := bus.NuevoRegistry([]bus.TipoRegistrado{{Tipo: "sesion.revocada", Version: 1}})
	d, err := Nuevo(c, reg, OpcionesDurable{AckWait: 1 * time.Second})
	if err != nil {
		t.Fatalf("nuevo durable: %v", err)
	}

	// Suscriptor de broadcast: representa a una réplica que debe enterarse.
	var mu sync.Mutex
	var recibido *bus.Sobre
	llegada := make(chan struct{}, 1)
	wSub, err := c.SubscribeBroadcast(func(ctx context.Context, s bus.Sobre) error {
		mu.Lock()
		cp := s
		recibido = &cp
		mu.Unlock()
		llegada <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("suscribir broadcast: %v", err)
	}

	// El reemisor: consumidor durable del stream amplio que difunde cada sobre.
	wReemisor, err := NuevoReemisor(d, c, "reemisor-broadcast", bus.SensibilidadAmplia, []string{"sesion.revocada"})
	if err != nil {
		t.Fatalf("nuevo reemisor: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = wSub.Run(ctx) }()
	go func() { _ = wReemisor.Run(ctx) }()
	time.Sleep(300 * time.Millisecond) // deja que ambos se suscriban

	// Publica al stream durable, como lo haría el relay de outbox de identidad.
	original := bus.Sobre{
		EventoID:     "rev-1",
		Tipo:         "sesion.revocada",
		Version:      1,
		AgregadoID:   "sesion-abc",
		Sensibilidad: bus.SensibilidadAmplia,
		OcurridoEn:   time.Now().UTC(),
		Payload:      []byte(`{"sesion_id":"sesion-abc"}`),
	}
	if err := d.Publish(ctx, original); err != nil {
		t.Fatalf("publish al durable: %v", err)
	}

	select {
	case <-llegada:
	case <-time.After(5 * time.Second):
		t.Fatal("el broadcast nunca llegó al suscriptor: el reemisor no difundió")
	}

	mu.Lock()
	defer mu.Unlock()
	if recibido == nil {
		t.Fatal("no se recibió sobre")
	}
	if recibido.EventoID != "rev-1" {
		t.Errorf("EventoID = %q, se esperaba rev-1", recibido.EventoID)
	}
	if recibido.Tipo != "sesion.revocada" {
		t.Errorf("Tipo = %q, se esperaba sesion.revocada", recibido.Tipo)
	}
	if recibido.AgregadoID != "sesion-abc" {
		t.Errorf("AgregadoID = %q, se esperaba sesion-abc", recibido.AgregadoID)
	}
	if string(recibido.Payload) != `{"sesion_id":"sesion-abc"}` {
		t.Errorf("Payload = %q, no coincide con el original", string(recibido.Payload))
	}
}
