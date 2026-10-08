package bus_test

import (
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
)

// TestNuevoRegistryAllowed verifica que Allowed acepta un (tipo, versión)
// presente y rechaza tanto un tipo ausente como una versión equivocada de un
// tipo conocido.
func TestNuevoRegistryAllowed(t *testing.T) {
	reg := bus.NuevoRegistry([]bus.TipoRegistrado{
		{Tipo: "viaje.estado_cambiado", Version: 1},
		{Tipo: "sesion.revocada", Version: 1},
	})

	casos := []struct {
		nombre   string
		tipo     string
		version  int
		esperado bool
	}{
		{"tipo y versión presentes", "viaje.estado_cambiado", 1, true},
		{"otro tipo presente", "sesion.revocada", 1, true},
		{"tipo ausente", "basura.tipo", 1, false},
		{"versión equivocada de tipo conocido", "viaje.estado_cambiado", 2, false},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := reg.Allowed(c.tipo, c.version); got != c.esperado {
				t.Errorf("Allowed(%q, %d) = %v, se esperaba %v", c.tipo, c.version, got, c.esperado)
			}
		})
	}
}

// TestNuevoRegistryOrderingKey verifica que OrderingKey devuelve el AgregadoID
// del sobre tal cual, sin recomponer la clave.
func TestNuevoRegistryOrderingKey(t *testing.T) {
	reg := bus.NuevoRegistry([]bus.TipoRegistrado{
		{Tipo: "viaje.estado_cambiado", Version: 1},
	})

	s := bus.Sobre{
		EventoID:   "e1",
		Tipo:       "viaje.estado_cambiado",
		Version:    1,
		AgregadoID: "V1",
		OcurridoEn: time.Now().UTC(),
		Payload:    []byte(`{}`),
	}
	if got := reg.OrderingKey(s); got != "V1" {
		t.Errorf("OrderingKey = %q, se esperaba %q", got, "V1")
	}
}

// TestNuevoRegistryVacio: un registry sin tipos no permite nada.
func TestNuevoRegistryVacio(t *testing.T) {
	reg := bus.NuevoRegistry(nil)
	if reg.Allowed("viaje.estado_cambiado", 1) {
		t.Error("un registry vacío no debe permitir ningún tipo")
	}
}
