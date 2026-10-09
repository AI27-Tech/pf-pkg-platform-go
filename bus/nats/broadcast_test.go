//go:build nats

// Estas pruebas corren contra un NATS REAL, no un doble (principle-prove-it-works).
// Llevan el build tag "nats" para no romper CI sin broker. Para correrlas:
//
//	docker run --rm -p 4222:4222 -p 8222:8222 nats:2-alpine -js -m 8222
//	go test -tags nats ./bus/nats/
//
// La URL del broker se toma de NATS_URL (default nats://127.0.0.1:4222).
package nats

import (
	"os"
	"testing"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/bus/bustest"
)

func urlNATS() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://127.0.0.1:4222"
}

// TestBroadcasterContrato ejerce la batería de contrato del Broadcaster contra
// un NATS real: dos réplicas suscritas deben recibir cada difusión (ADR 29 §3).
func TestBroadcasterContrato(t *testing.T) {
	bustest.EjecutarBroadcaster(t, func(t *testing.T) bus.Broadcaster {
		c, err := Conectar(urlNATS())
		if err != nil {
			t.Fatalf("conectar a NATS: %v", err)
		}
		t.Cleanup(func() { _ = c.Cerrar() })
		return c
	})
}
