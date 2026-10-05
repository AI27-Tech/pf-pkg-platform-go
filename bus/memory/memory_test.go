package memory_test

import (
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	"github.com/AI27-Tech/pf-pkg-platform-go/bus/bustest"
	"github.com/AI27-Tech/pf-pkg-platform-go/bus/memory"
)

// TestContrato corre toda la batería del puerto contra el adaptador en memoria.
func TestContrato(t *testing.T) {
	bustest.Ejecutar(t, func(t *testing.T, reg bus.Registry) bustest.Adaptador {
		b := memory.Nuevo(reg, memory.Opciones{Backoff: time.Millisecond})
		return bustest.Adaptador{
			Publisher:  b,
			Subscriber: b,
			Cuarentena: b.EnCuarentena,
		}
	})
}

// TestContratoBroadcaster corre la batería del Broadcaster.
func TestContratoBroadcaster(t *testing.T) {
	bustest.EjecutarBroadcaster(t, func(t *testing.T) bus.Broadcaster {
		return memory.Nuevo(registryTodo{}, memory.Opciones{})
	})
}

// registryTodo acepta cualquier tipo; para el broadcaster, que no filtra por
// registro.
type registryTodo struct{}

func (registryTodo) Allowed(tipo string, version int) bool { return true }
func (registryTodo) OrderingKey(s bus.Sobre) string        { return s.AgregadoID }
