// Este archivo cubre el reemisor de difusión (ADR 29 §3): el consumidor durable
// que lee eventos del stream y los reemite por el Broadcaster a TODAS las
// réplicas. Es la pieza que convierte un evento durable (una sola entrega, con
// acuse) en una difusión efímera (fan-out a cada réplica viva).
//
// Por qué existe (ADR 29 §3): sesion.revocada se publica al stream durable por
// la outbox del productor (identidad). Pero la revocación debe llegar a TODAS
// las réplicas de un módulo protegido para que cada una invalide su proyección
// local; una suscripción durable entregaría el evento a UNA sola. El reemisor
// cierra esa brecha: consume el durable una vez y lo grita por el broadcast.
//
// Es puro transporte: no toca base de datos ni conoce el dominio. Vive en
// platform-go para que cualquier módulo lo instancie con los tipos que deba
// reemitir. El durable del reemisor es independiente de los consumidores reales
// del mismo stream: en JetStream cada consumidor durable recibe todos los
// mensajes, así que reemitir no le roba entregas a nadie.
package nats

import (
	"context"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
)

// NuevoReemisor crea el worker reemisor: un consumidor durable sobre el stream
// de la superficie dada que, por cada sobre recibido, lo difunde con el
// Broadcaster. durableName identifica al consumidor durable del reemisor (p. ej.
// "reemisor-broadcast"); filtroTipos restringe qué tipos reemite (vacío = toda
// la superficie).
//
// El worker resultante lo arranca la raíz en su RunGroup. Al cancelar el
// contexto, drena igual que cualquier consumidor durable. Si el Broadcast falla
// (conexión caída), el handler devuelve error: el sobre no se acusa y se
// reintenta, de modo que una difusión perdida por una caída transitoria se
// reintente al volver.
func NuevoReemisor(durable *Durable, difusor bus.Broadcaster, durableName string, superficie bus.Sensibilidad, filtroTipos []string) (worker.Worker, error) {
	reemitir := func(ctx context.Context, s bus.Sobre) error {
		return difusor.Broadcast(ctx, s)
	}
	return durable.Subscribe(durableName, superficie, filtroTipos, reemitir)
}
