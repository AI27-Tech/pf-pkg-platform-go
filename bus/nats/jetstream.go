// Este archivo cubre el consumidor DURABLE del bus sobre NATS JetStream
// (E0-T10): Publisher y Subscriber. Es la contraparte durable del Broadcaster
// efímero de broadcast.go, sobre la misma Conexion.
//
// A diferencia del broadcast, aquí todo persiste: un stream por superficie
// (amplia/restringida) retiene los eventos, y cada módulo consume por una
// suscripción durable propia que sobrevive reinicios (ADR 29 §2, ADR 32 §1).
//
// Garantías que cumple (verificadas por bus/bustest.Ejecutar):
//   - Orden por agregado SIN clave global (decisión A2): el sujeto incluye la
//     clave de orden, ai27.<sensibilidad>.<tipo>.<orderingKey>. JetStream
//     entrega en orden dentro de un sujeto; agregados distintos van en sujetos
//     distintos y no se serializan entre sí.
//   - Venenoso (tipo/versión no registrado) a cuarentena ANTES de acusar; nunca
//     llega al handler.
//   - Reintentos acotados (MaxIntentos, default 5) con el redelivery de
//     JetStream; agotados, el sobre va a cuarentena (DLQ por módulo) y se saca
//     del flujo con Term para no bloquear el sujeto.
//   - Acuse (Ack) DESPUÉS de que el handler devuelve nil (acuse tras commit).
//   - Separación amplia/restringida: cada suscripción filtra por su superficie.
//
// La deduplicación por EventoID NO vive aquí: la hace el handler del módulo con
// inbox.Registrar en su transacción (ADR 29 §1, ADR 34). El adaptador entrega
// at-least-once y pasa el EventoID intacto.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
)

// prefijoDurable es el espacio de nombres de los sujetos durables. Separado de
// ai27.broadcast.* (fan-out efímero) a propósito: aquí todo persiste en un
// stream. El sujeto completo es ai27.<sensibilidad>.<tipo>.<orderingKey>.
const prefijoDurable = "ai27."

// OpcionesDurable ajusta el comportamiento del consumidor durable. Los valores
// de arranque son los decididos para V1 (5 intentos, 30s de espera de acuse) y
// son configurables: la raíz los puede sobreescribir por ambiente (ADR 29 §4,
// que deja los números como configuración operativa).
type OpcionesDurable struct {
	// MaxIntentos es el máximo de entregas antes de mandar el sobre a cuarentena
	// (DLQ por módulo). Default 5.
	MaxIntentos int
	// AckWait es cuánto espera el servidor el acuse antes de reentregar. Default
	// 30s. Debe cubrir el peor caso de procesamiento del handler más su commit.
	AckWait time.Duration
	// MaxAgeStream es la retención de cada stream de superficie. Default 0 (sin
	// expiración por edad: la retención real la fija operaciones por ambiente).
	MaxAgeStream time.Duration
	// Replicas es el número de réplicas de cada stream en el clúster. Default 1
	// para desarrollo; en V1 on-premise es 3 (ADR 32 §3), lo fija la raíz.
	Replicas int
}

func (o OpcionesDurable) conDefaults() OpcionesDurable {
	if o.MaxIntentos <= 0 {
		o.MaxIntentos = 5
	}
	if o.AckWait <= 0 {
		o.AckWait = 30 * time.Second
	}
	if o.Replicas <= 0 {
		o.Replicas = 1
	}
	return o
}

// sanearSujeto reemplaza los caracteres que NATS reserva en un token de sujeto
// (espacio, punto, asterisco, mayor-que) para que una clave de orden arbitraria
// (p. ej. "entidad id" con espacio) no rompa el espacio de sujetos ni cambie su
// jerarquía. El punto es el separador de tokens, así que no puede ir dentro de
// un token de dato.
func sanearSujeto(token string) string {
	return strings.NewReplacer(" ", "_", ".", "_", "*", "_", ">", "_").Replace(token)
}

// sujetoDurable arma el sujeto de publicación de un sobre (decisión A2): la
// superficie, el tipo y la clave de orden, de modo que el orden por agregado lo
// garantice JetStream por sujeto.
func sujetoDurable(sens bus.Sensibilidad, tipo, orderingKey string) string {
	return prefijoDurable + string(sens) + "." + tipo + "." + sanearSujeto(orderingKey)
}

// nombreStream es el stream de una superficie: ai27_amplia o ai27_restringida.
// Un stream por superficie (ADR 29 §2); captura ai27.<sensibilidad>.> .
func nombreStream(sens bus.Sensibilidad) string {
	return "ai27_" + string(sens)
}

// Durable extiende la Conexion con los puertos durables. Se construye con Nuevo
// a partir de una Conexion ya establecida; comparte su *nats.Conn. Mantiene el
// cliente JetStream y la contabilidad de cuarentena por módulo.
type Durable struct {
	js   jetstream.JetStream
	reg  bus.Registry
	opts OpcionesDurable

	mu         sync.Mutex
	cuarentena map[string][]string // módulo -> EventoIDs en cuarentena
}

var (
	_ bus.Publisher  = (*Durable)(nil)
	_ bus.Subscriber = (*Durable)(nil)
)

// Nuevo construye el adaptador durable sobre una Conexion existente, con el
// Registry que aporta el módulo (desde el contrato) y las opciones dadas. El
// Registry decide qué tipo/versión es válido (venenoso si no) y la clave de
// orden de cada sobre.
func Nuevo(c *Conexion, reg bus.Registry, opts OpcionesDurable) (*Durable, error) {
	js, err := jetstream.New(c.nc)
	if err != nil {
		return nil, fmt.Errorf("abriendo JetStream: %w", err)
	}
	return &Durable{
		js:         js,
		reg:        reg,
		opts:       opts.conDefaults(),
		cuarentena: map[string][]string{},
	}, nil
}

// asegurarStream crea (o actualiza) el stream de una superficie, idempotente.
// Lo llaman tanto Publish como Subscribe, de modo que el primero en correr lo
// cree y el resto lo reutilice.
func (d *Durable) asegurarStream(ctx context.Context, sens bus.Sensibilidad) error {
	_, err := d.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      nombreStream(sens),
		Subjects:  []string{prefijoDurable + string(sens) + ".>"},
		Retention: jetstream.LimitsPolicy,
		Storage:   jetstream.FileStorage,
		MaxAge:    d.opts.MaxAgeStream,
		Replicas:  d.opts.Replicas,
	})
	if err != nil {
		return fmt.Errorf("asegurando stream %s: %w", nombreStream(sens), err)
	}
	return nil
}

// Publish entrega el sobre al stream de su superficie. Un tipo/versión no
// registrado es venenoso: va directo a cuarentena y NO se publica (el productor
// no debe emitir algo que ningún consumidor podrá aceptar). Devuelve nil solo
// cuando JetStream acusó la persistencia: así el outbox.Relay marca la fila
// publicada solo con entrega garantizada (at-least-once).
func (d *Durable) Publish(ctx context.Context, s bus.Sobre) error {
	if s.Sensibilidad == "" {
		s.Sensibilidad = bus.SensibilidadAmplia
	}

	if !d.reg.Allowed(s.Tipo, s.Version) {
		// Venenoso contractual: a cuarentena sin productor atribuible a un
		// módulo consumidor. Se registra bajo "productor" y no se publica.
		d.registrarCuarentena("productor", s.EventoID)
		return nil
	}

	if err := d.asegurarStream(ctx, s.Sensibilidad); err != nil {
		return err
	}

	datos, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("serializando sobre %q: %w", s.EventoID, err)
	}

	sujeto := sujetoDurable(s.Sensibilidad, s.Tipo, d.reg.OrderingKey(s))
	// Entrega at-least-once honesta: no se pide dedup del transporte. La
	// idempotencia por EventoID la hace el consumidor con inbox.Registrar en su
	// transacción (ADR 29 §1, ADR 34); el adaptador no debe fingir exactly-once
	// con la ventana de dedup limitada de JetStream, porque un redrive fuera de
	// esa ventana reintroduciría el duplicado y el consumidor que confiara en el
	// transporte perdería el efecto único.
	if _, err := d.js.Publish(ctx, sujeto, datos); err != nil {
		return fmt.Errorf("publicando %q en %q: %w", s.EventoID, sujeto, err)
	}
	return nil
}

// Subscribe crea un consumidor durable para el módulo durableName sobre la
// superficie dada. filtroTipos, si no está vacío, restringe los tipos
// entregados. Devuelve un worker.Worker que la raíz arranca en su RunGroup.
func (d *Durable) Subscribe(durableName string, superficie bus.Sensibilidad, filtroTipos []string, h bus.Handler) (worker.Worker, error) {
	if durableName == "" {
		return nil, fmt.Errorf("durableName no debe estar vacío")
	}
	return &consumidorDurable{
		d:          d,
		modulo:     durableName,
		superficie: superficie,
		filtro:     filtroTipos,
		handler:    h,
	}, nil
}

// Cuarentena devuelve todos los EventoID en cuarentena, de todos los módulos
// (hook para bus/bustest). Un adaptador de producción expondría además el
// desglose por módulo para el redrive auditado (ADR 29 §4).
func (d *Durable) Cuarentena() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, ids := range d.cuarentena {
		out = append(out, ids...)
	}
	return out
}

// registrarCuarentena anota un EventoID en la cuarentena del módulo. Solo
// metadatos (EventoID), nunca el payload sensible (ADR 29 §4).
func (d *Durable) registrarCuarentena(modulo, eventoID string) {
	d.mu.Lock()
	d.cuarentena[modulo] = append(d.cuarentena[modulo], eventoID)
	d.mu.Unlock()
}

// consumidorDurable es la suscripción durable de UN módulo. Implementa
// worker.Worker: Run abre el consumidor JetStream y entrega hasta que ctx se
// cancele.
type consumidorDurable struct {
	d          *Durable
	modulo     string
	superficie bus.Sensibilidad
	filtro     []string
	handler    bus.Handler
}

func (c *consumidorDurable) Name() string { return "bus-durable:" + c.modulo }

func (c *consumidorDurable) Ready(ctx context.Context) error {
	if !c.d.js.Conn().IsConnected() {
		return fmt.Errorf("conexión NATS no disponible")
	}
	return nil
}

// filterSubjects traduce la superficie y el filtro de tipos a los sujetos que el
// consumidor durable escucha. Sin filtro: toda la superficie (ai27.<sens>.>).
// Con filtro: un sujeto por tipo (ai27.<sens>.<tipo>.>), para que el servidor
// entregue solo esos tipos.
func (c *consumidorDurable) filterSubjects() []string {
	base := prefijoDurable + string(c.superficie)
	if len(c.filtro) == 0 {
		return []string{base + ".>"}
	}
	subs := make([]string, 0, len(c.filtro))
	for _, t := range c.filtro {
		subs = append(subs, base+"."+t+".>")
	}
	return subs
}

// Run asegura el stream de la superficie, crea el consumidor durable del módulo
// y consume hasta que ctx se cancele. Al cancelar, detiene el consumo y
// devuelve nil sin acusar trabajo no terminado (JetStream reentregará lo no
// acusado al rearrancar: la suscripción es durable).
func (c *consumidorDurable) Run(ctx context.Context) error {
	if err := c.d.asegurarStream(ctx, c.superficie); err != nil {
		return err
	}

	cfg := jetstream.ConsumerConfig{
		Durable:        c.modulo,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        c.d.opts.AckWait,
		MaxDeliver:     c.d.opts.MaxIntentos,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		FilterSubjects: c.filterSubjects(),
		// MaxAckPending 1 preserva el orden por sujeto: el servidor no entrega el
		// siguiente mensaje de este consumidor hasta acusar el actual. Como cada
		// agregado vive en su propio sujeto (A2), el costo de serializar es por
		// consumidor, no por agregado; para V1 (un consumidor por módulo) es el
		// comportamiento correcto y verificable. Un refinamiento por partición
		// queda para cuando haya evidencia de que un módulo necesita paralelismo.
		MaxAckPending: 1,
	}
	cons, err := c.d.js.CreateOrUpdateConsumer(ctx, nombreStream(c.superficie), cfg)
	if err != nil {
		return fmt.Errorf("creando consumidor durable %q: %w", c.modulo, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		c.procesar(ctx, msg)
	})
	if err != nil {
		return fmt.Errorf("consumiendo en %q: %w", c.modulo, err)
	}
	defer cc.Stop()

	<-ctx.Done()
	return nil
}

// procesar entrega un mensaje al handler y acusa según el resultado. El acuse
// (Ack) ocurre solo tras un handler exitoso (acuse tras commit). Un fallo se
// NAK-ea para reentrega; al llegar al último intento permitido, el sobre va a
// cuarentena y se saca del flujo con Term para no bloquear su sujeto.
func (c *consumidorDurable) procesar(ctx context.Context, msg jetstream.Msg) {
	var s bus.Sobre
	if err := json.Unmarshal(msg.Data(), &s); err != nil {
		// Mensaje ilegible: no es reintentable. A cuarentena y fuera del flujo.
		c.aCuarentena(msg, "")
		return
	}

	if err := c.handler(ctx, s); err != nil {
		md, mdErr := msg.Metadata()
		if mdErr == nil && md.NumDelivered >= uint64(c.d.opts.MaxIntentos) {
			// Agotó los intentos: a cuarentena, fuera del flujo.
			c.aCuarentena(msg, s.EventoID)
			return
		}
		// Fallo transitorio: reentregar tras el backoff del servidor.
		_ = msg.Nak()
		return
	}

	_ = msg.Ack()
}

// aCuarentena registra el EventoID en la cuarentena del módulo y saca el mensaje
// del flujo con Term (no se reentrega). Term confirma la cuarentena ANTES de que
// el sujeto avance, como exige ADR 29 §4.
func (c *consumidorDurable) aCuarentena(msg jetstream.Msg, eventoID string) {
	c.d.registrarCuarentena(c.modulo, eventoID)
	_ = msg.Term()
}
