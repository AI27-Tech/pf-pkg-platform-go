// Package worker define la unidad de trabajo embebida de un módulo y un
// ejecutor que arranca un grupo de workers, registra su readiness y espera el
// drenado al apagar.
//
// Un worker es de UN módulo (ADR 29 §6): la outbox.Relay, un consumidor durable
// del bus, o cualquier tarea en segundo plano. El módulo los construye con su
// propio rol y pool; la raíz de composición decide arrancarlos (00-indice.md).
// Este paquete no conoce pools, roles ni esquemas: solo orquesta el ciclo de
// vida.
package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/AI27-Tech/pf-pkg-platform-go/health"
)

// Worker es una unidad de trabajo embebida de UN módulo (ADR 29 §6).
type Worker interface {
	// Name es un nombre estable; etiqueta de métricas y logs.
	Name() string
	// Run bloquea hasta que ctx se cancele. Al cancelar, drena sin acusar
	// trabajo no terminado y devuelve nil (o el error que impidió drenar).
	Run(ctx context.Context) error
	// Ready es compatible con health.CheckFunc: nil si el worker está sano.
	Ready(ctx context.Context) error
}

// RunGroup arranca todos los workers, registra el Ready de cada uno en el
// registro de health y bloquea hasta que ctx se cancele. Al cancelar, espera a
// que todos drenen antes de volver.
//
// Si algún worker devuelve un error distinto de context.Canceled, RunGroup
// cancela a los demás (apagado coordinado) y devuelve ese error tras el
// drenado. Los nombres de los workers deben ser únicos.
func RunGroup(ctx context.Context, reg *health.Registry, workers ...Worker) error {
	if len(workers) == 0 {
		return nil
	}

	// Validar nombres únicos y registrar readiness antes de arrancar.
	vistos := make(map[string]struct{}, len(workers))
	for _, w := range workers {
		name := w.Name()
		if _, dup := vistos[name]; dup {
			return fmt.Errorf("worker: nombre duplicado %q", name)
		}
		vistos[name] = struct{}{}
		if reg != nil {
			w := w // captura por iteración
			reg.RegisterCheck("worker:"+name, w.Ready)
		}
	}

	// Contexto del grupo: se cancela cuando el padre se cancela o cuando el
	// primer worker falla, para apagar a los demás de forma coordinada.
	grupoCtx, cancelar := context.WithCancel(ctx)
	defer cancelar()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		primero error
	)

	for _, w := range workers {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := w.Run(grupoCtx)
			if err != nil && !errors.Is(err, context.Canceled) {
				mu.Lock()
				if primero == nil {
					primero = fmt.Errorf("worker %q: %w", w.Name(), err)
				}
				mu.Unlock()
				cancelar() // apaga a los demás
			}
		}()
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	return primero
}
