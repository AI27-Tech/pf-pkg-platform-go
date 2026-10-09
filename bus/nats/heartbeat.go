// heartbeat.go emite el latido del canal de difusión (ADR 26 §5). La frescura
// del canal NO puede inferirse de la llegada de revocaciones, porque puede no
// haber ninguna durante un periodo sano; por eso un emisor difunde un latido
// periódico y cada réplica consumidora mide frescura por "cuándo llegó la última
// señal del canal" (sea un latido o un evento real).
//
// Es puro transporte sobre el Broadcaster, igual que el reemisor: no toca base
// de datos ni dominio. Vive en platform-go para que la raíz lo instancie.
package nats

import (
	"context"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
)

// TipoHeartbeat es el Tipo reservado del sobre de latido. El consumidor lo
// reconoce para contarlo como señal de frescura del canal SIN tratarlo como un
// tombstone de sesión. Viaja por el broadcast, que no pasa por el Registry, así
// que no necesita estar en el registro de tipos del contrato.
const TipoHeartbeat = "bus.heartbeat"

// IntervaloHeartbeatPorDefecto es el periodo del latido. 5s deja que hagan falta
// tres latidos perdidos seguidos antes de cruzar el umbral de obsolescencia de
// 15s (ADR 26 §5): un solo latido perdido no dispara el fallback. Configurable.
const IntervaloHeartbeatPorDefecto = 5 * time.Second

// NuevoEmisorHeartbeat crea el worker que difunde un latido por el Broadcaster
// cada intervalo. Si intervalo <= 0, usa IntervaloHeartbeatPorDefecto. El worker
// lo arranca la raíz en su RunGroup; al cancelar el contexto, se detiene.
func NuevoEmisorHeartbeat(difusor bus.Broadcaster, intervalo time.Duration) worker.Worker {
	if intervalo <= 0 {
		intervalo = IntervaloHeartbeatPorDefecto
	}
	return &emisorHeartbeat{difusor: difusor, intervalo: intervalo}
}

type emisorHeartbeat struct {
	difusor   bus.Broadcaster
	intervalo time.Duration
}

func (e *emisorHeartbeat) Name() string { return "bus-heartbeat" }

func (e *emisorHeartbeat) Ready(_ context.Context) error { return nil }

// Run difunde un latido cada intervalo hasta que ctx se cancele. Un fallo de
// Broadcast (conexión caída) no detiene el emisor: el próximo tick reintenta.
// La ausencia de latido es, por diseño, lo que el consumidor interpreta como
// canal obsoleto, así que no hace falta propagar el error aquí.
func (e *emisorHeartbeat) Run(ctx context.Context) error {
	ticker := time.NewTicker(e.intervalo)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = e.difusor.Broadcast(ctx, bus.Sobre{
				Tipo:         TipoHeartbeat,
				OcurridoEn:   time.Now().UTC(),
				Sensibilidad: bus.SensibilidadRestringida,
			})
		}
	}
}
