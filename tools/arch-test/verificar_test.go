package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reglasDePrueba(t *testing.T) *Reglas {
	t.Helper()
	r, err := cargarReglas()
	if err != nil {
		t.Fatalf("cargar reglas: %v", err)
	}
	return r
}

// escribirGoMod crea un go.mod mínimo con la ruta de módulo dada y los requires.
func escribirGoMod(t *testing.T, modulePath string, requires ...string) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("module " + modulePath + "\n\ngo 1.27\n")
	if len(requires) > 0 {
		b.WriteString("\nrequire (\n")
		for _, r := range requires {
			b.WriteString("\t" + r + " v0.1.0\n")
		}
		b.WriteString(")\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("escribir go.mod: %v", err)
	}
	return dir
}

func contiene(problemas []string, substr string) bool {
	for _, p := range problemas {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

// TestServicioDesconocidoFalla: una ruta de módulo que no mapea a un servicio
// del grafo ni a core debe producir un problema (00 regla 5).
func TestServicioDesconocidoFalla(t *testing.T) {
	dir := escribirGoMod(t, "github.com/AI27-Tech/pf-svc-inventado")
	problemas, err := verificar(dir, reglasDePrueba(t))
	if err != nil {
		t.Fatalf("verificar: %v", err)
	}
	if !contiene(problemas, "no corresponde a ningún servicio") {
		t.Errorf("un servicio desconocido debería fallar; problemas = %v", problemas)
	}
}

// TestServicioRequiereOtroServicioFalla: un pf-svc-* (no core) cuyo go.mod
// requiere otro pf-svc-* viola 00 regla 5a.
func TestServicioRequiereOtroServicioFalla(t *testing.T) {
	dir := escribirGoMod(t,
		"github.com/AI27-Tech/pf-svc-operacion",
		"github.com/AI27-Tech/pf-svc-catalogos", // ¡prohibido!
	)
	problemas, err := verificar(dir, reglasDePrueba(t))
	if err != nil {
		t.Fatalf("verificar: %v", err)
	}
	if !contiene(problemas, "requiere otro servicio") {
		t.Errorf("requerir otro servicio debería fallar; problemas = %v", problemas)
	}
}

// TestCoreePuedeRequerirServicios: pf-svc-core SÍ puede requerir otros servicios.
func TestCorePuedeRequerirServicios(t *testing.T) {
	dir := escribirGoMod(t,
		"github.com/AI27-Tech/pf-svc-core",
		"github.com/AI27-Tech/pf-svc-operacion",
		"github.com/AI27-Tech/pf-svc-catalogos",
	)
	problemas, err := verificar(dir, reglasDePrueba(t))
	if err != nil {
		t.Fatalf("verificar: %v", err)
	}
	if contiene(problemas, "requiere otro servicio") {
		t.Errorf("core debería poder requerir servicios; problemas = %v", problemas)
	}
}

// TestServicioValidoSinImportsPasa: un servicio del grafo sin paquetes todavía
// no produce violaciones.
func TestServicioValidoSinImportsPasa(t *testing.T) {
	dir := escribirGoMod(t, "github.com/AI27-Tech/pf-svc-identidad")
	problemas, err := verificar(dir, reglasDePrueba(t))
	if err != nil {
		t.Fatalf("verificar: %v", err)
	}
	if len(problemas) != 0 {
		t.Errorf("identidad vacío no debería tener problemas; problemas = %v", problemas)
	}
}

// TestRolesEsPaqueteCompartido: el enum de roles (DEC-053, generado en tracer.3)
// debe estar declarado como paquete compartido; si no, arch-test lo tomaría por
// una arista hacia un servicio "roles" inexistente y marcaría un falso positivo
// en cualquier módulo que verifique tokens (p. ej. identidad).
func TestRolesEsPaqueteCompartido(t *testing.T) {
	compartidos := reglasDePrueba(t).paquetesCompartidos()
	for _, p := range []string{"estados", "eventos", "roles"} {
		if !compartidos[p] {
			t.Errorf("%q debería estar en shared_packages; compartidos = %v", p, compartidos)
		}
	}
}

// TestPaquetesCompartidosNoSonAristas: un import de un paquete compartido no
// debe producir una violación de arista, mientras que un import de un servicio
// no declarado sí. Se ejercita la lógica de clasificación directamente, sin
// depender de `go list`.
func TestPaquetesCompartidosNoSonAristas(t *testing.T) {
	r := reglasDePrueba(t)
	compartidos := r.paquetesCompartidos()
	permitidas := r.aristasPermitidas("identidad") // identidad es hoja: ninguna

	// Simula lo que importsDeClientes extrae de `go list -deps`: el primer
	// segmento tras el prefijo del cliente generado.
	clasificar := func(segmento string) (esArista, esViolacion bool) {
		if compartidos[segmento] {
			return false, false // compartido: nunca es arista
		}
		return true, !permitidas[segmento]
	}

	if _, v := clasificar("roles"); v {
		t.Error("importar el paquete compartido roles no debería ser una violación")
	}
	if _, v := clasificar("operacion"); !v {
		t.Error("identidad (hoja) importando el cliente de operacion debería ser una violación")
	}
}

// TestGrafoDeclaradoEsAciclico: el reglas.yaml embebido no debe tener ciclos.
func TestGrafoDeclaradoEsAciclico(t *testing.T) {
	if p := verificarAciclico(reglasDePrueba(t)); len(p) != 0 {
		t.Errorf("el grafo declarado debería ser acíclico; %v", p)
	}
}

// TestDeteccionDeCiclo: si introducimos un ciclo artificial, se detecta.
func TestDeteccionDeCiclo(t *testing.T) {
	r := reglasDePrueba(t)
	// identidad no tiene aristas; crear identidad -> operacion -> catalogos ->
	// identidad para forzar un ciclo.
	r.AllowedEdges["identidad"] = []string{"operacion"}
	r.AllowedEdges["catalogos"] = append(r.AllowedEdges["catalogos"], "identidad")
	if p := verificarAciclico(r); len(p) == 0 {
		t.Error("debería detectar el ciclo introducido")
	}
}
