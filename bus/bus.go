// Package bus define el puerto agnóstico del bus de eventos de dominio de
// AI27 (ADR 29). No fija transporte: V1 corre on-premise y su broker lo elige
// ADR 32 (propuesta: NATS JetStream), que todavía está en Propuesta. Aquí viven
// las interfaces y las garantías que todo adaptador debe cumplir; los
// adaptadores concretos (en memoria para desarrollo y pruebas, broker para
// producción) viven en subpaquetes.
//
// platform-go NO conoce el dominio: no importa pf-pkg-contracts ni nombres de
// evento. Qué tipos/versiones son válidos y cómo se deriva la clave de orden lo
// aporta el módulo a través de un Registry inyectado (generado desde el
// registro de tipos del contrato, C5).
//
// Garantías del puerto (ADR 29; ADR 32 §1), que bus/bustest verifica:
//   - Suscripción durable POR consumidor durable, es decir, por módulo
//     (DEC-057); nunca compartida entre módulos del mismo proceso.
//   - Orden por clave de agregado (OrderingKey); no hay clave global.
//   - Deduplicación por EventoID (en coordinación con inbox).
//   - Tema amplio o restringido según Sensibilidad.
//   - Tipo/versión no registrado = venenoso: se confirma en DLQ/cuarentena
//     ANTES de acusar el original.
//   - El acuse ocurre DESPUÉS de que el handler hace commit.
//   - Reintentos acotados con backoff configurable (valores de arranque
//     provisionales, ADR 29 §4).
package bus

import (
	"context"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
)

// Sensibilidad clasifica el evento en la superficie amplia o restringida del
// bus (ADR 29). Coincide con el enum del contrato y con outbox.Sensibilidad.
type Sensibilidad string

const (
	SensibilidadAmplia      Sensibilidad = "amplia"
	SensibilidadRestringida Sensibilidad = "restringida"
)

// Sobre es la unidad que viaja por el bus. Reutiliza los campos de la capa de
// dominio del sobre (los mismos que outbox.Event y que `Sobre` del contrato)
// más el carrier de traza, de modo que el trazo continúe HTTP → outbox → bus →
// consumidor.
type Sobre struct {
	// EventoID identifica el evento y es la clave de idempotencia del consumidor.
	EventoID string
	// Tipo es el nombre del tipo de evento, p. ej. "viaje.estado_cambiado".
	Tipo string
	// Version es la versión del contrato del payload.
	Version int
	// AgregadoID es la identidad del agregado del que deriva el orden.
	AgregadoID string
	// Sensibilidad indica la superficie del bus (amplia por defecto).
	Sensibilidad Sensibilidad
	// OcurridoEn es el tiempo de dominio del hecho (UTC).
	OcurridoEn time.Time
	// Payload es el cuerpo del evento ya serializado (JSON).
	Payload []byte
	// TraceContext es el carrier W3C (traceparent/tracestate) para continuar el
	// trazo. Puede ir vacío.
	TraceContext map[string]string
}

// Registry lo aporta el MÓDULO (desde el registro generado del contrato, C5).
// El bus no conoce nombres de evento: le pregunta al registry qué es válido y
// cómo ordenar. Mantener este puerto estrecho evita que platform-go dependa del
// dominio.
type Registry interface {
	// Allowed indica si un tipo/versión está registrado. Un no registrado es
	// venenoso y va a cuarentena.
	Allowed(tipo string, version int) bool
	// OrderingKey devuelve la clave de orden de un sobre. Por defecto es el
	// AgregadoID, pero el módulo puede refinarla. Nunca debe ser global.
	OrderingKey(s Sobre) string
}

// Publisher publica sobres en el bus. Lo usa outbox.Relay tras drenar la tabla.
type Publisher interface {
	// Publish entrega el sobre al transporte. Devuelve error si el transporte
	// no acepta la entrega; el llamador decide reintentar (la outbox no marca
	// publicado hasta el acuse).
	Publish(ctx context.Context, s Sobre) error
}

// Handler procesa un sobre entregado a un consumidor durable. Debe ser
// idempotente respecto de EventoID (en coordinación con inbox). Si devuelve
// error, el sobre se reintenta; agotados los reintentos, va a cuarentena.
type Handler func(ctx context.Context, s Sobre) error

// Subscriber crea consumidores durables. Un consumidor durable es un
// worker.Worker: lo arranca la raíz dentro de un worker.RunGroup.
type Subscriber interface {
	// Subscribe crea un consumidor durable identificado por durableName (el
	// nombre del MÓDULO, DEC-057), sobre la superficie dada (amplia o
	// restringida), que entrega al handler respetando las garantías del puerto.
	// filtroTipos, si no está vacío, restringe los tipos entregados; vacío
	// significa toda la superficie.
	Subscribe(durableName string, superficie Sensibilidad, filtroTipos []string, h Handler) (worker.Worker, error)
}

// Broadcaster difunde un sobre a TODAS las réplicas (fan-out sin durabilidad),
// no a un consumidor durable. Es el puerto de sesion.revocada (ADR 26 §3,
// ADR 29 §3): toda réplica debe invalidar su proyección local. La implementación
// real sobre el broker es de E0-T4; aquí va el puerto y su doble en memoria.
type Broadcaster interface {
	// Broadcast entrega el sobre a cada réplica suscrita en este instante.
	Broadcast(ctx context.Context, s Sobre) error
	// SubscribeBroadcast crea un consumidor efímero (no durable) de la difusión.
	SubscribeBroadcast(h Handler) (worker.Worker, error)
}
