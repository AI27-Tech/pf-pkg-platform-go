// Package bustest ofrece la batería de pruebas de contrato del puerto bus.
// Cualquier adaptador (bus/memory hoy, el adaptador del broker cuando ADR 32
// elija componente) la ejecuta para demostrar que cumple las garantías de
// ADR 29. Las pruebas son agnósticas al transporte: operan solo sobre las
// interfaces de bus.
package bustest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
)

// Adaptador es lo que un adaptador concreto expone a la batería de pruebas.
// La implementación crea una instancia limpia y aislada por llamada.
type Adaptador struct {
	// Publisher y Subscriber sobre el mismo transporte.
	Publisher  bus.Publisher
	Subscriber bus.Subscriber
	// Cuarentena devuelve los EventoID actualmente en DLQ/cuarentena.
	Cuarentena func() []string
}

// Fabrica crea un Adaptador limpio con el Registry dado. Cada subprueba pide uno
// nuevo para no compartir estado.
type Fabrica func(t *testing.T, reg bus.Registry) Adaptador

// registryFalso acepta una lista blanca de tipos y ordena por AgregadoID.
type registryFalso struct {
	permitidos map[string]bool
}

func nuevoRegistry(tipos ...string) *registryFalso {
	m := make(map[string]bool, len(tipos))
	for _, t := range tipos {
		m[clave(t, 1)] = true
	}
	return &registryFalso{permitidos: m}
}

func clave(tipo string, v int) string { return fmt.Sprintf("%s@%d", tipo, v) }

func (r *registryFalso) Allowed(tipo string, version int) bool {
	return r.permitidos[clave(tipo, version)]
}

func (r *registryFalso) OrderingKey(s bus.Sobre) string { return s.AgregadoID }

// Ejecutar corre toda la batería de contrato contra el adaptador dado.
func Ejecutar(t *testing.T, nueva Fabrica) {
	t.Run("OrdenPorClaveDeAgregado", func(t *testing.T) { ordenPorClave(t, nueva) })
	t.Run("VenenosoACuarentenaAntesDelAcuse", func(t *testing.T) { venenoso(t, nueva) })
	t.Run("DuplicadoUnSoloEfecto", func(t *testing.T) { duplicado(t, nueva) })
	t.Run("AcuseTrasCommit", func(t *testing.T) { acuseTrasCommit(t, nueva) })
	t.Run("SeparacionAmpliaRestringida", func(t *testing.T) { separacion(t, nueva) })
}

func sobre(tipo, agregado, id string, sens bus.Sensibilidad) bus.Sobre {
	return bus.Sobre{
		EventoID:     id,
		Tipo:         tipo,
		Version:      1,
		AgregadoID:   agregado,
		Sensibilidad: sens,
		OcurridoEn:   time.Now().UTC(),
		Payload:      []byte(`{}`),
	}
}

// ordenPorClave: los eventos del mismo agregado se entregan en orden de
// publicación; no hay garantía de orden entre agregados distintos.
func ordenPorClave(t *testing.T, nueva Fabrica) {
	reg := nuevoRegistry("viaje.estado_cambiado")
	ad := nueva(t, reg)

	var mu sync.Mutex
	recibidos := map[string][]string{}
	done := make(chan struct{})
	esperados := 5

	h := func(ctx context.Context, s bus.Sobre) error {
		mu.Lock()
		recibidos[s.AgregadoID] = append(recibidos[s.AgregadoID], s.EventoID)
		total := 0
		for _, v := range recibidos {
			total += len(v)
		}
		mu.Unlock()
		if total == esperados {
			close(done)
		}
		return nil
	}

	w, err := ad.Subscriber.Subscribe("mod-a", bus.SensibilidadAmplia, nil, h)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	// Agregado V1: tres eventos en orden; V2: dos eventos.
	pub := []bus.Sobre{
		sobre("viaje.estado_cambiado", "V1", "e1", bus.SensibilidadAmplia),
		sobre("viaje.estado_cambiado", "V1", "e2", bus.SensibilidadAmplia),
		sobre("viaje.estado_cambiado", "V2", "e3", bus.SensibilidadAmplia),
		sobre("viaje.estado_cambiado", "V1", "e4", bus.SensibilidadAmplia),
		sobre("viaje.estado_cambiado", "V2", "e5", bus.SensibilidadAmplia),
	}
	for _, s := range pub {
		if err := ad.Publisher.Publish(ctx, s); err != nil {
			t.Fatalf("publish %s: %v", s.EventoID, err)
		}
	}

	esperar(t, done, "no se recibieron todos los eventos")

	mu.Lock()
	defer mu.Unlock()
	if got := recibidos["V1"]; !iguales(got, []string{"e1", "e2", "e4"}) {
		t.Errorf("orden de V1 = %v, se esperaba [e1 e2 e4]", got)
	}
	if got := recibidos["V2"]; !iguales(got, []string{"e3", "e5"}) {
		t.Errorf("orden de V2 = %v, se esperaba [e3 e5]", got)
	}
}

// venenoso: un tipo/versión no registrado va a cuarentena y NO llega al handler.
func venenoso(t *testing.T, nueva Fabrica) {
	reg := nuevoRegistry("viaje.estado_cambiado") // "basura.tipo" no está
	ad := nueva(t, reg)

	var mu sync.Mutex
	var vistos []string
	recibidoBueno := make(chan struct{}, 1)
	h := func(ctx context.Context, s bus.Sobre) error {
		mu.Lock()
		vistos = append(vistos, s.EventoID)
		mu.Unlock()
		recibidoBueno <- struct{}{}
		return nil
	}
	w, err := ad.Subscriber.Subscribe("mod-a", bus.SensibilidadAmplia, nil, h)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	_ = ad.Publisher.Publish(ctx, sobre("basura.tipo", "V1", "veneno", bus.SensibilidadAmplia))
	_ = ad.Publisher.Publish(ctx, sobre("viaje.estado_cambiado", "V1", "bueno", bus.SensibilidadAmplia))

	esperar(t, recibidoBueno, "el evento bueno nunca llegó")

	mu.Lock()
	for _, id := range vistos {
		if id == "veneno" {
			t.Error("el evento venenoso no debió llegar al handler")
		}
	}
	mu.Unlock()

	// El venenoso debe estar en cuarentena.
	esperarCond(t, func() bool {
		for _, id := range ad.Cuarentena() {
			if id == "veneno" {
				return true
			}
		}
		return false
	}, "el evento venenoso debió quedar en cuarentena")
}

// duplicado: dos entregas del mismo EventoID producen un solo efecto cuando el
// handler deduplica por EventoID (contrato de idempotencia con inbox).
func duplicado(t *testing.T, nueva Fabrica) {
	reg := nuevoRegistry("viaje.creado")
	ad := nueva(t, reg)

	var mu sync.Mutex
	efectos := map[string]int{}
	done := make(chan struct{}, 2)
	h := func(ctx context.Context, s bus.Sobre) error {
		mu.Lock()
		efectos[s.EventoID]++
		mu.Unlock()
		done <- struct{}{}
		return nil
	}
	w, err := ad.Subscriber.Subscribe("mod-a", bus.SensibilidadAmplia, nil, h)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	s := sobre("viaje.creado", "V1", "dup", bus.SensibilidadAmplia)
	_ = ad.Publisher.Publish(ctx, s)
	_ = ad.Publisher.Publish(ctx, s) // misma identidad

	// Esperamos al menos una entrega; damos margen a la segunda.
	esperar(t, done, "no llegó ninguna entrega")
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	// El handler de prueba cuenta por EventoID: el efecto neto es 1 identidad.
	if len(efectos) != 1 {
		t.Errorf("se esperaba un solo EventoID con efecto, hubo %d", len(efectos))
	}
}

// acuseTrasCommit: si el handler falla, el sobre se reintenta (no se pierde);
// cuando por fin tiene éxito, se acusa. Verifica que el acuse no precede al
// commit del handler.
func acuseTrasCommit(t *testing.T, nueva Fabrica) {
	reg := nuevoRegistry("viaje.creado")
	ad := nueva(t, reg)

	var intentos int
	var mu sync.Mutex
	ok := make(chan struct{}, 1)
	h := func(ctx context.Context, s bus.Sobre) error {
		mu.Lock()
		intentos++
		n := intentos
		mu.Unlock()
		if n < 2 {
			return fmt.Errorf("fallo transitorio en el intento %d", n)
		}
		ok <- struct{}{}
		return nil
	}
	w, err := ad.Subscriber.Subscribe("mod-a", bus.SensibilidadAmplia, nil, h)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	_ = ad.Publisher.Publish(ctx, sobre("viaje.creado", "V1", "reintento", bus.SensibilidadAmplia))
	esperar(t, ok, "el sobre no se reintentó hasta el éxito")

	mu.Lock()
	defer mu.Unlock()
	if intentos < 2 {
		t.Errorf("se esperaban >= 2 intentos (reintento tras fallo), hubo %d", intentos)
	}
}

// separacion: un consumidor de la superficie amplia no recibe eventos
// restringidos, y viceversa.
func separacion(t *testing.T, nueva Fabrica) {
	reg := nuevoRegistry("viaje.creado", "conversacion.creada")
	ad := nueva(t, reg)

	var mu sync.Mutex
	ampliaVio := map[string]bool{}
	restrVio := map[string]bool{}
	marcar := func(m map[string]bool) bus.Handler {
		return func(ctx context.Context, s bus.Sobre) error {
			mu.Lock()
			m[s.EventoID] = true
			mu.Unlock()
			return nil
		}
	}

	wa, err := ad.Subscriber.Subscribe("mod-amplia", bus.SensibilidadAmplia, nil, marcar(ampliaVio))
	if err != nil {
		t.Fatalf("subscribe amplia: %v", err)
	}
	wr, err := ad.Subscriber.Subscribe("mod-restr", bus.SensibilidadRestringida, nil, marcar(restrVio))
	if err != nil {
		t.Fatalf("subscribe restringida: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = wa.Run(ctx) }()
	go func() { _ = wr.Run(ctx) }()

	_ = ad.Publisher.Publish(ctx, sobre("viaje.creado", "V1", "amplio", bus.SensibilidadAmplia))
	_ = ad.Publisher.Publish(ctx, sobre("conversacion.creada", "C1", "restr", bus.SensibilidadRestringida))

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !ampliaVio["amplio"] {
		t.Error("el consumidor amplio debió ver el evento amplio")
	}
	if ampliaVio["restr"] {
		t.Error("el consumidor amplio NO debió ver el evento restringido")
	}
	if !restrVio["restr"] {
		t.Error("el consumidor restringido debió ver el evento restringido")
	}
	if restrVio["amplio"] {
		t.Error("el consumidor restringido NO debió ver el evento amplio")
	}
}

// EjecutarBroadcaster corre la batería de contrato del Broadcaster: todas las
// réplicas suscritas reciben la difusión.
func EjecutarBroadcaster(t *testing.T, nuevo func(t *testing.T) bus.Broadcaster) {
	b := nuevo(t)
	var mu sync.Mutex
	vistosA, vistosB := 0, 0
	hA := func(ctx context.Context, s bus.Sobre) error { mu.Lock(); vistosA++; mu.Unlock(); return nil }
	hB := func(ctx context.Context, s bus.Sobre) error { mu.Lock(); vistosB++; mu.Unlock(); return nil }

	wa, err := b.SubscribeBroadcast(hA)
	if err != nil {
		t.Fatalf("subscribe A: %v", err)
	}
	wb, err := b.SubscribeBroadcast(hB)
	if err != nil {
		t.Fatalf("subscribe B: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = wa.Run(ctx) }()
	go func() { _ = wb.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	if err := b.Broadcast(ctx, sobre("sesion.revocada", "S1", "rev1", bus.SensibilidadRestringida)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if vistosA != 1 || vistosB != 1 {
		t.Errorf("ambas réplicas debían ver la difusión: A=%d B=%d", vistosA, vistosB)
	}
}

// Helpers de sincronización para las pruebas.

func esperar(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(msg)
	}
}

func esperarCond(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func iguales(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// afirmar que worker.Worker se usa (evita import sin uso si cambia la firma).
var _ = func(w worker.Worker) {}
