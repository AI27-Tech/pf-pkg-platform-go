// Package nats es el adaptador del puerto bus sobre NATS (ADR 32, propuesta de
// broker V1). Este archivo cubre SOLO el Broadcaster (E0-T4): la difusión de
// sesion.revocada a todas las réplicas vivas (ADR 26 §3, ADR 29 §3).
//
// El broadcast es fan-out EFÍMERO, no durable: usa NATS core pub/sub, no
// JetStream. Una réplica que no está suscrita en el instante de la difusión no
// la recibe, y así debe ser: una réplica nueva converge por snapshot + cursor
// (ADR 26), no por el broadcast. Por eso este adaptador no tiene Registry,
// clave de orden, cuarentena ni reintentos; nada de eso aplica a la difusión.
// El consumidor durable (Publisher/Subscriber sobre JetStream) es E0-T10 y vive
// en otro archivo de este paquete.
package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
)

// prefijoBroadcast es el espacio de nombres de los sujetos de difusión. Se
// mantiene separado del canal durable (ai27.<sensibilidad>.<tipo>) para que un
// sujeto de fan-out efímero no se confunda con uno durable de JetStream.
const prefijoBroadcast = "ai27.broadcast."

// sujetoBroadcast deriva el sujeto NATS de un tipo de evento: el broadcast de
// "sesion.revocada" viaja por "ai27.broadcast.sesion.revocada".
func sujetoBroadcast(tipo string) string {
	return prefijoBroadcast + tipo
}

// Conexion envuelve una conexión NATS y expone los puertos del bus sobre ella.
// Hoy implementa Broadcaster (E0-T4); Publisher/Subscriber durables se suman en
// E0-T10. El dueño de la conexión la cierra con Cerrar al apagar la raíz.
type Conexion struct {
	nc *nats.Conn
}

var _ bus.Broadcaster = (*Conexion)(nil)

// Conectar abre una conexión NATS a la URL dada (p. ej. "nats://127.0.0.1:4222")
// y devuelve la Conexion lista para usarse. El llamador debe cerrarla con
// Cerrar. opciones permite inyectar ajustes de nats.go (credenciales, TLS,
// nombre); puede ir vacío.
func Conectar(url string, opciones ...nats.Option) (*Conexion, error) {
	nc, err := nats.Connect(url, opciones...)
	if err != nil {
		return nil, fmt.Errorf("conectando a NATS en %q: %w", url, err)
	}
	return &Conexion{nc: nc}, nil
}

// Desde envuelve una conexión NATS ya establecida. Útil cuando la raíz gestiona
// la conexión por su cuenta (reconexión, instrumentación) y solo quiere el
// adaptador encima.
func Desde(nc *nats.Conn) *Conexion {
	return &Conexion{nc: nc}
}

// Cerrar drena y cierra la conexión NATS. Drenar entrega lo pendiente antes de
// cerrar, de modo que un apagado ordenado no corte difusiones en vuelo.
func (c *Conexion) Cerrar() error {
	if err := c.nc.Drain(); err != nil {
		return fmt.Errorf("drenando conexión NATS: %w", err)
	}
	return nil
}

// Broadcast publica el sobre en el sujeto de difusión de su tipo. Entrega a
// cada réplica suscrita en este instante; no espera acuse ni persiste. Un error
// aquí es de publicación (conexión caída), no de entrega extremo a extremo.
func (c *Conexion) Broadcast(ctx context.Context, s bus.Sobre) error {
	datos, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("serializando sobre %q: %w", s.EventoID, err)
	}
	if err := c.nc.Publish(sujetoBroadcast(s.Tipo), datos); err != nil {
		return fmt.Errorf("publicando difusión de %q: %w", s.Tipo, err)
	}
	return nil
}

// SubscribeBroadcast crea un consumidor efímero de la difusión. Se suscribe a
// todos los sujetos de broadcast (ai27.broadcast.>) y entrega cada sobre al
// handler. Devuelve un worker.Worker: la raíz lo arranca en su RunGroup y, al
// cancelar el contexto, el worker se desuscribe y drena sin dejar la suscripción
// colgada.
func (c *Conexion) SubscribeBroadcast(h bus.Handler) (worker.Worker, error) {
	return &suscriptorBroadcast{nc: c.nc, handler: h}, nil
}

// suscriptorBroadcast es un consumidor efímero de la difusión. No es durable:
// solo recibe lo que se publica mientras está suscrito.
type suscriptorBroadcast struct {
	nc      *nats.Conn
	handler bus.Handler
}

func (s *suscriptorBroadcast) Name() string { return "bus-broadcast" }

// Ready reporta sano mientras la conexión NATS esté conectada.
func (s *suscriptorBroadcast) Ready(ctx context.Context) error {
	if !s.nc.IsConnected() {
		return fmt.Errorf("conexión NATS no disponible")
	}
	return nil
}

// Run se suscribe al patrón de difusión y bloquea hasta que ctx se cancele. Al
// cancelar, se desuscribe (drenando los mensajes ya entregados al buffer) y
// devuelve nil. Usa una suscripción con canal para no perder la secuencia entre
// la recepción y el despacho al handler.
func (s *suscriptorBroadcast) Run(ctx context.Context) error {
	entregas := make(chan *nats.Msg, 128)
	sub, err := s.nc.ChanSubscribe(prefijoBroadcast+">", entregas)
	if err != nil {
		return fmt.Errorf("suscribiendo a difusión: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-entregas:
			var sobre bus.Sobre
			if err := json.Unmarshal(msg.Data, &sobre); err != nil {
				// Un mensaje ilegible en el canal de difusión no debe tumbar al
				// worker: se descarta. No hay cuarentena en fan-out efímero.
				continue
			}
			_ = s.handler(ctx, sobre)
		}
	}
}
