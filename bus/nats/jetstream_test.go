//go:build nats

// Pruebas del adaptador durable contra un NATS JetStream REAL, no un doble
// (principle-prove-it-works). Llevan el build tag "nats" para no romper CI sin
// broker. Para correrlas:
//
//	docker run --rm -p 4222:4222 -p 8222:8222 nats:2-alpine -js -m 8222
//	go test -tags nats ./bus/nats/
//
// La URL del broker se toma de NATS_URL (default nats://127.0.0.1:4222).
package nats

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/bus/bustest"
)

// TestDurableContrato ejerce la batería de contrato de Publisher+Subscriber
// contra JetStream real: orden por agregado, venenoso a cuarentena antes del
// acuse, duplicado con un solo efecto, acuse tras commit, y separación
// amplia/restringida (ADR 29).
func TestDurableContrato(t *testing.T) {
	bustest.Ejecutar(t, func(t *testing.T, reg bus.Registry) bustest.Adaptador {
		c, err := Conectar(urlNATS())
		if err != nil {
			t.Fatalf("conectar a NATS: %v", err)
		}
		t.Cleanup(func() { _ = c.Cerrar() })

		// Estado limpio por subprueba: los streams de JetStream persisten en el
		// servidor entre pruebas, así que se borran antes de cada adaptador. Un
		// AckWait corto acelera el reintento del caso AcuseTrasCommit.
		purgarStreams(t, c)
		d, err := Nuevo(c, reg, OpcionesDurable{AckWait: 1 * time.Second})
		if err != nil {
			t.Fatalf("nuevo durable: %v", err)
		}
		return bustest.Adaptador{
			Publisher:  d,
			Subscriber: d,
			Cuarentena: d.Cuarentena,
		}
	})
}

// purgarStreams borra los streams de superficie si existen, para que cada
// subprueba arranque sin eventos ni consumidores de una corrida anterior.
func purgarStreams(t *testing.T, c *Conexion) {
	t.Helper()
	js, err := jetstream.New(c.nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, sens := range []bus.Sensibilidad{bus.SensibilidadAmplia, bus.SensibilidadRestringida} {
		nombre := nombreStream(sens)
		if err := js.DeleteStream(ctx, nombre); err != nil && err != jetstream.ErrStreamNotFound {
			t.Logf("purga de %s: %v (se ignora si no existía)", nombre, err)
		}
	}
}
