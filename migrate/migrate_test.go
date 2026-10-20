package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgconn"
)

// ejecutorEspia registra el orden de las sentencias aplicadas y con qué rol, sin
// tocar una base real. Basta para afirmar el orden de dos fases y la asociación
// rol→esquema, que es la lógica del aplicador; el SQL real se ejerce en el test
// de integración.
type ejecutorEspia struct {
	rol        string
	aplicadas  *[]aplicada // compartido entre los ejecutores de una corrida
	errAlAchar string      // si el SQL contiene este substring, Exec falla
}

type aplicada struct {
	rol string
	sql string
}

func (e ejecutorEspia) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if e.errAlAchar != "" && strings.Contains(sql, e.errAlAchar) {
		return pgconn.CommandTag{}, errors.New("fallo simulado")
	}
	*e.aplicadas = append(*e.aplicadas, aplicada{rol: e.rol, sql: strings.TrimSpace(sql)})
	return pgconn.CommandTag{}, nil
}

// moduloOperacionFalso reproduce la forma de operacion: cinco esquemas, cada uno
// con 0001 (estructura) y 0002 (grants cruzados), más un 0003 en viajes. El
// contenido de cada .sql es una marca legible para afirmar el orden.
func moduloOperacionFalso() (Modulo, fstest.MapFS) {
	fs := fstest.MapFS{
		"viajes/0001_init.sql":       {Data: []byte("viajes.init")},
		"viajes/0002_grants.sql":     {Data: []byte("viajes.grants")},
		"viajes/0003_ficha.sql":      {Data: []byte("viajes.ficha")},
		"bitacora/0001_init.sql":     {Data: []byte("bitacora.init")},
		"bitacora/0002_grants.sql":   {Data: []byte("bitacora.grants")},
		"asignacion/0001_init.sql":   {Data: []byte("asignacion.init")},
		"asignacion/0002_grants.sql": {Data: []byte("asignacion.grants")},
	}
	m := Modulo{
		Nombre: "operacion",
		Esquemas: []Esquema{
			{Nombre: "viajes", Rol: "op_viajes", FS: fs, Dir: "viajes"},
			{Nombre: "bitacora", Rol: "op_bitacora", FS: fs, Dir: "bitacora"},
			{Nombre: "asignacion", Rol: "op_asignacion", FS: fs, Dir: "asignacion"},
		},
	}
	return m, fs
}

func resolverEspia(aplicadas *[]aplicada, errAlAchar string) ResolverEjecutor {
	return func(rol string) (Ejecutor, error) {
		return ejecutorEspia{rol: rol, aplicadas: aplicadas, errAlAchar: errAlAchar}, nil
	}
}

// TestAplicarDosFases es el corazón: todos los 0001 (fase 1) deben aplicarse
// ANTES que cualquier 0002+ (fase 2), respetando el orden de esquemas dentro de
// cada fase. Es la regla que la excepción de GRANT de operacion obliga.
func TestAplicarDosFases(t *testing.T) {
	m, _ := moduloOperacionFalso()
	var aplicadas []aplicada
	if err := Aplicar(context.Background(), m, resolverEspia(&aplicadas, "")); err != nil {
		t.Fatalf("Aplicar devolvió error: %v", err)
	}

	quiero := []aplicada{
		// Fase 1: el 0001 de cada esquema en orden de módulo.
		{rol: "op_viajes", sql: "viajes.init"},
		{rol: "op_bitacora", sql: "bitacora.init"},
		{rol: "op_asignacion", sql: "asignacion.init"},
		// Fase 2: el resto de cada esquema en orden de módulo y de nombre.
		{rol: "op_viajes", sql: "viajes.grants"},
		{rol: "op_viajes", sql: "viajes.ficha"},
		{rol: "op_bitacora", sql: "bitacora.grants"},
		{rol: "op_asignacion", sql: "asignacion.grants"},
	}
	if len(aplicadas) != len(quiero) {
		t.Fatalf("se aplicaron %d archivos, se esperaban %d: %+v", len(aplicadas), len(quiero), aplicadas)
	}
	for i := range quiero {
		if aplicadas[i] != quiero[i] {
			t.Errorf("paso %d: apliqué %+v, esperaba %+v", i, aplicadas[i], quiero[i])
		}
	}
}

// TestAplicarUnEsquema confirma el caso simple (reportes/identidad): un esquema,
// archivos en orden de nombre. Fase 1 = 0001, fase 2 = el resto.
func TestAplicarUnEsquema(t *testing.T) {
	fs := fstest.MapFS{
		"reportes/0001_init.sql":     {Data: []byte("r.init")},
		"reportes/0002_grants.sql":   {Data: []byte("r.grants")},
		"reportes/0003_unidades.sql": {Data: []byte("r.unidades")},
	}
	m := Modulo{Nombre: "reportes", Esquemas: []Esquema{
		{Nombre: "reportes", Rol: "svc_reportes", FS: fs, Dir: "reportes"},
	}}
	var aplicadas []aplicada
	if err := Aplicar(context.Background(), m, resolverEspia(&aplicadas, "")); err != nil {
		t.Fatalf("Aplicar: %v", err)
	}
	quiero := []string{"r.init", "r.grants", "r.unidades"}
	if len(aplicadas) != len(quiero) {
		t.Fatalf("apliqué %d, esperaba %d", len(aplicadas), len(quiero))
	}
	for i, s := range quiero {
		if aplicadas[i].sql != s || aplicadas[i].rol != "svc_reportes" {
			t.Errorf("paso %d: %+v", i, aplicadas[i])
		}
	}
}

// TestValidaAntesDeAplicar: un esquema vacío hace fallar la corrida SIN aplicar
// nada (falla antes, no a mitad dejando la base a medias).
func TestValidaAntesDeAplicar(t *testing.T) {
	fs := fstest.MapFS{
		"viajes/0001_init.sql": {Data: []byte("viajes.init")},
		// "vacio" no tiene archivos
	}
	m := Modulo{Nombre: "operacion", Esquemas: []Esquema{
		{Nombre: "viajes", Rol: "op_viajes", FS: fs, Dir: "viajes"},
		{Nombre: "vacio", Rol: "op_vacio", FS: fs, Dir: "vacio"},
	}}
	var aplicadas []aplicada
	err := Aplicar(context.Background(), m, resolverEspia(&aplicadas, ""))
	if err == nil {
		t.Fatal("se esperaba error por esquema vacío")
	}
	if len(aplicadas) != 0 {
		t.Fatalf("no debió aplicar nada al fallar la validación previa, aplicó %d: %+v", len(aplicadas), aplicadas)
	}
}

// TestRolSinEjecutor: si el resolvedor no puede dar un ejecutor para un rol, la
// corrida falla antes de aplicar nada.
func TestRolSinEjecutor(t *testing.T) {
	m, _ := moduloOperacionFalso()
	var aplicadas []aplicada
	resolver := func(rol string) (Ejecutor, error) {
		if rol == "op_asignacion" {
			return nil, errors.New("sin credencial para op_asignacion")
		}
		return ejecutorEspia{rol: rol, aplicadas: &aplicadas}, nil
	}
	err := Aplicar(context.Background(), m, resolver)
	if err == nil {
		t.Fatal("se esperaba error por rol sin ejecutor")
	}
	if len(aplicadas) != 0 {
		t.Fatalf("no debió aplicar nada: %+v", aplicadas)
	}
}

// TestErrorEnFaseDosSePropaga: un fallo al aplicar un archivo se propaga (no se
// traga). Verifica que el error de Exec sube.
func TestErrorEnFaseDosSePropaga(t *testing.T) {
	m, _ := moduloOperacionFalso()
	var aplicadas []aplicada
	// Falla al aplicar los grants de asignacion.
	err := Aplicar(context.Background(), m, resolverEspia(&aplicadas, "asignacion.grants"))
	if err == nil {
		t.Fatal("se esperaba que el error de Exec se propagara")
	}
}
