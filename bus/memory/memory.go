// Package memory es un adaptador en memoria del puerto bus para desarrollo y
// pruebas. NUNCA es para producción: no persiste, no sobrevive al reinicio y
// corre en un solo proceso. Cumple las garantías de ADR 29 que verifica
// bus/bustest, de modo que el código de los módulos no distingue este adaptador
// del broker real.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
)

// Opciones ajusta reintentos. Los valores de arranque son provisionales
// (ADR 29 §4); un adaptador real los toma de configuración.
type Opciones struct {
	MaxIntentos int           // intentos antes de cuarentena (default 3)
	Backoff     time.Duration // espera base entre reintentos (default 5ms en memoria)
}

func (o Opciones) conDefaults() Opciones {
	if o.MaxIntentos <= 0 {
		o.MaxIntentos = 3
	}
	if o.Backoff <= 0 {
		o.Backoff = 5 * time.Millisecond
	}
	return o
}

// Bus es el adaptador en memoria. Implementa Publisher, Subscriber y
// Broadcaster sobre el mismo transporte.
type Bus struct {
	reg  bus.Registry
	opts Opciones

	mu           sync.Mutex
	consumidores []*consumidor
	broadcast    []*difusion
	cuarentena   []string
}

var (
	_ bus.Publisher   = (*Bus)(nil)
	_ bus.Subscriber  = (*Bus)(nil)
	_ bus.Broadcaster = (*Bus)(nil)
)

// Nuevo crea un bus en memoria con el Registry inyectado por el módulo.
func Nuevo(reg bus.Registry, opts Opciones) *Bus {
	return &Bus{reg: reg, opts: opts.conDefaults()}
}

// Publish entrega el sobre a cada consumidor durable cuya superficie coincide.
// Un tipo/versión no registrado es venenoso: va directo a cuarentena y no se
// entrega a ningún handler.
func (b *Bus) Publish(ctx context.Context, s bus.Sobre) error {
	if s.Sensibilidad == "" {
		s.Sensibilidad = bus.SensibilidadAmplia
	}

	if !b.reg.Allowed(s.Tipo, s.Version) {
		b.mu.Lock()
		b.cuarentena = append(b.cuarentena, s.EventoID)
		b.mu.Unlock()
		return nil // confirmado en cuarentena antes de cualquier acuse
	}

	b.mu.Lock()
	destinos := make([]*consumidor, 0, len(b.consumidores))
	for _, c := range b.consumidores {
		if c.acepta(s) {
			destinos = append(destinos, c)
		}
	}
	b.mu.Unlock()

	for _, c := range destinos {
		c.encolar(s)
	}
	return nil
}

// Subscribe crea un consumidor durable (un worker.Worker) para el módulo
// durableName sobre la superficie dada.
func (b *Bus) Subscribe(durableName string, superficie bus.Sensibilidad, filtroTipos []string, h bus.Handler) (worker.Worker, error) {
	filtro := make(map[string]bool, len(filtroTipos))
	for _, t := range filtroTipos {
		filtro[t] = true
	}
	c := &consumidor{
		bus:        b,
		nombre:     durableName,
		superficie: superficie,
		filtro:     filtro,
		handler:    h,
		colas:      map[string]*cola{},
		despertar:  make(chan struct{}, 1),
		reg:        b.reg,
		opts:       b.opts,
	}
	b.mu.Lock()
	b.consumidores = append(b.consumidores, c)
	b.mu.Unlock()
	return c, nil
}

// Broadcast entrega el sobre a cada réplica suscrita a la difusión.
func (b *Bus) Broadcast(ctx context.Context, s bus.Sobre) error {
	b.mu.Lock()
	destinos := make([]*difusion, len(b.broadcast))
	copy(destinos, b.broadcast)
	b.mu.Unlock()
	for _, d := range destinos {
		d.entregar(s)
	}
	return nil
}

// SubscribeBroadcast crea un consumidor efímero de la difusión.
func (b *Bus) SubscribeBroadcast(h bus.Handler) (worker.Worker, error) {
	d := &difusion{nombre: "broadcast", handler: h, cola: make(chan bus.Sobre, 128)}
	b.mu.Lock()
	b.broadcast = append(b.broadcast, d)
	b.mu.Unlock()
	return d, nil
}

// EnCuarentena devuelve los EventoID actualmente en cuarentena (para bustest).
func (b *Bus) EnCuarentena() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.cuarentena))
	copy(out, b.cuarentena)
	return out
}

// cola es una FIFO de sobres de UNA clave de agregado; preserva el orden.
type cola struct {
	items []bus.Sobre
}

// consumidor es una suscripción durable. Mantiene una cola por clave de
// agregado para garantizar orden por clave sin bloquear entre agregados.
type consumidor struct {
	bus        *Bus
	nombre     string
	superficie bus.Sensibilidad
	filtro     map[string]bool
	handler    bus.Handler
	reg        bus.Registry
	opts       Opciones

	mu        sync.Mutex
	colas     map[string]*cola
	orden     []string // claves en orden de primera aparición, para recorrido estable
	despertar chan struct{}
}

func (c *consumidor) Name() string { return c.nombre }

func (c *consumidor) Ready(ctx context.Context) error { return nil }

func (c *consumidor) acepta(s bus.Sobre) bool {
	if s.Sensibilidad != c.superficie {
		return false
	}
	if len(c.filtro) > 0 && !c.filtro[s.Tipo] {
		return false
	}
	return true
}

func (c *consumidor) encolar(s bus.Sobre) {
	key := c.reg.OrderingKey(s)
	c.mu.Lock()
	q, ok := c.colas[key]
	if !ok {
		q = &cola{}
		c.colas[key] = q
		c.orden = append(c.orden, key)
	}
	q.items = append(q.items, s)
	c.mu.Unlock()
	select {
	case c.despertar <- struct{}{}:
	default:
	}
}

// Run drena las colas respetando el orden por clave. Al cancelar ctx, deja de
// procesar sin acusar lo que no terminó.
func (c *consumidor) Run(ctx context.Context) error {
	for {
		procesó := c.procesarUnaRonda(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if procesó {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-c.despertar:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// procesarUnaRonda toma el primer sobre de cada clave y lo entrega. Devuelve
// true si procesó algo.
func (c *consumidor) procesarUnaRonda(ctx context.Context) bool {
	c.mu.Lock()
	claves := make([]string, len(c.orden))
	copy(claves, c.orden)
	c.mu.Unlock()

	hizo := false
	for _, key := range claves {
		if ctx.Err() != nil {
			return hizo
		}
		c.mu.Lock()
		q := c.colas[key]
		if q == nil || len(q.items) == 0 {
			c.mu.Unlock()
			continue
		}
		s := q.items[0]
		c.mu.Unlock()

		if c.entregarConReintentos(ctx, s) {
			// Acuse: recién ahora quitamos el sobre de la cola (acuse tras commit).
			c.mu.Lock()
			if len(q.items) > 0 && q.items[0].EventoID == s.EventoID {
				q.items = q.items[1:]
			}
			c.mu.Unlock()
		} else if ctx.Err() == nil {
			// Agotó reintentos: a cuarentena y se descarta de la cola.
			c.bus.mu.Lock()
			c.bus.cuarentena = append(c.bus.cuarentena, s.EventoID)
			c.bus.mu.Unlock()
			c.mu.Lock()
			if len(q.items) > 0 && q.items[0].EventoID == s.EventoID {
				q.items = q.items[1:]
			}
			c.mu.Unlock()
		}
		hizo = true
	}
	return hizo
}

// entregarConReintentos llama al handler hasta MaxIntentos. Devuelve true si el
// handler tuvo éxito (procede el acuse), false si agotó los intentos.
func (c *consumidor) entregarConReintentos(ctx context.Context, s bus.Sobre) bool {
	for intento := 1; intento <= c.opts.MaxIntentos; intento++ {
		if ctx.Err() != nil {
			return false
		}
		if err := c.handler(ctx, s); err == nil {
			return true
		}
		if intento < c.opts.MaxIntentos {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(c.opts.Backoff * time.Duration(intento)):
			}
		}
	}
	return false
}

// difusion es un consumidor efímero de Broadcast.
type difusion struct {
	nombre  string
	handler bus.Handler
	cola    chan bus.Sobre
}

func (d *difusion) Name() string { return d.nombre }

func (d *difusion) Ready(ctx context.Context) error { return nil }

func (d *difusion) entregar(s bus.Sobre) {
	select {
	case d.cola <- s:
	default:
	}
}

func (d *difusion) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case s := <-d.cola:
			_ = d.handler(ctx, s)
		}
	}
}
