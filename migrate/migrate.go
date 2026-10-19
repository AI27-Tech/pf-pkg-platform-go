// Package migrate aplica las migraciones SQL que cada módulo embebe, respetando
// el orden entre esquemas y la separación en dos fases que la excepción de GRANT
// de operacion obliga (ADR 33). No abre conexiones ni elige credenciales: el
// llamador le pasa un Ejecutor por esquema, de modo que el bootstrap de
// despliegue corra con credencial administrativa y el servicio nunca porte
// privilegio de CREATE SCHEMA/CREATE ROLE (DEC-096, invariante I1 de ADR 17).
//
// platform-go no administra schemas: cada módulo trae los suyos como un embed.FS
// (00 "Forma de un módulo"). Este paquete solo los ejecuta en el orden correcto.
//
// Las dos fases. Un módulo de un solo esquema (reportes, identidad, catalogos,
// mensajeria) se aplica en el orden de nombre de sus archivos y no necesita más.
// operacion reparte CINCO esquemas cuyo primer archivo (0001) crea el esquema y
// su rol, y cuyos archivos siguientes (0002+) otorgan grants que CRUZAN esquemas:
// viajes/0002 otorga a op_asignacion, pero ese rol lo crea asignacion/0001, el
// último del orden. Aplicar esquema por esquema de principio a fin haría que
// viajes/0002 otorgara a un rol inexistente. Por eso se aplica en dos fases a
// través de los esquemas: primero el 0001 de cada uno (estructura: esquemas,
// roles, tablas), luego el resto de cada uno (grants y migraciones posteriores).
// Para un módulo de un esquema las dos fases dan el mismo resultado que el orden
// secuencial, así que la regla es uniforme.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Ejecutor ejecuta una sentencia SQL. Lo cumple *pgxpool.Pool y pgx.Conn/Tx sin
// adaptador (su Exec tiene esta firma). El aplicador lo recibe ya resuelto para
// que la credencial (quién se conecta) la decida el llamador, no este paquete
// (DEC-096). Al aplicar no se inspecciona el CommandTag, solo el error.
type Ejecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Esquema es una unidad de migración: un esquema de base de datos, el rol que lo
// posee y el sistema de archivos con sus .sql. Dir es el subdirectorio dentro de
// FS que contiene los archivos (p. ej. "viajes"); coincide con el nombre del
// esquema por convención, pero se nombra aparte por si divergen.
type Esquema struct {
	Nombre string // esquema de BD, p. ej. "viajes"
	Rol    string // rol dueño, p. ej. "op_viajes" (clave para resolver el Ejecutor)
	FS     fs.FS  // sistema de archivos embebido del módulo
	Dir    string // subdirectorio en FS con los .sql (p. ej. "viajes")
}

// Modulo es el conjunto ordenado de esquemas de un módulo. El orden importa por
// las dependencias de GRANT entre esquemas (operacion: roster antes que
// asignacion; viajes/bitacora/auditoria antes que asignacion). Un módulo de un
// solo esquema trae un solo elemento.
type Modulo struct {
	Nombre   string // nombre del módulo, para diagnósticos (p. ej. "operacion")
	Esquemas []Esquema
}

// ResolverEjecutor devuelve el Ejecutor con el que aplicar las migraciones del
// rol dado. El bootstrap de despliegue devuelve aquí una conexión con credencial
// administrativa (permiso de CREATE SCHEMA/CREATE ROLE); nunca el pool de app del
// servicio (DEC-096). Si un rol no tiene ejecutor, Aplicar falla antes de tocar
// la base.
type ResolverEjecutor func(rol string) (Ejecutor, error)

// Aplicar ejecuta las migraciones del módulo en dos fases (ver doc del paquete):
// primero el primer archivo de cada esquema en orden, luego el resto de cada
// esquema en orden. Cada esquema se aplica con el Ejecutor que resolver asocie a
// su rol dueño. Es idempotente en la medida en que lo sean los .sql (los de AI27
// usan IF NOT EXISTS / CREATE OR REPLACE y guardas de rol): reaplicar no debe
// fallar ni duplicar efecto.
func Aplicar(ctx context.Context, m Modulo, resolver ResolverEjecutor) error {
	if len(m.Esquemas) == 0 {
		return fmt.Errorf("migrate: módulo %q sin esquemas", m.Nombre)
	}

	// Resolver y validar los archivos de cada esquema por adelantado: si algo
	// falta (un esquema vacío, un rol sin ejecutor), se falla antes de aplicar
	// nada, no a mitad de camino dejando la base a medias.
	type esquemaResuelto struct {
		esq      Esquema
		archivos []string // ordenados por nombre
		ej       Ejecutor
	}
	resueltos := make([]esquemaResuelto, 0, len(m.Esquemas))
	for _, e := range m.Esquemas {
		archivos, err := archivosDe(e.FS, e.Dir)
		if err != nil {
			return fmt.Errorf("migrate: módulo %q, esquema %q: %w", m.Nombre, e.Nombre, err)
		}
		if len(archivos) == 0 {
			return fmt.Errorf("migrate: módulo %q, esquema %q: sin archivos .sql en %q", m.Nombre, e.Nombre, e.Dir)
		}
		ej, err := resolver(e.Rol)
		if err != nil {
			return fmt.Errorf("migrate: módulo %q, esquema %q: resolver ejecutor del rol %q: %w", m.Nombre, e.Nombre, e.Rol, err)
		}
		if ej == nil {
			return fmt.Errorf("migrate: módulo %q, esquema %q: ejecutor nil para el rol %q", m.Nombre, e.Nombre, e.Rol)
		}
		resueltos = append(resueltos, esquemaResuelto{esq: e, archivos: archivos, ej: ej})
	}

	// Fase 1: el primer archivo (estructura: esquema, rol, tablas) de cada
	// esquema, en el orden del módulo.
	for _, r := range resueltos {
		if err := aplicarArchivo(ctx, r.ej, r.esq, r.archivos[0]); err != nil {
			return err
		}
	}

	// Fase 2: el resto de cada esquema (grants cruzados y migraciones
	// posteriores), en el mismo orden.
	for _, r := range resueltos {
		for _, nombre := range r.archivos[1:] {
			if err := aplicarArchivo(ctx, r.ej, r.esq, nombre); err != nil {
				return err
			}
		}
	}

	return nil
}

// aplicarArchivo lee un .sql del FS del esquema y lo ejecuta como una sola
// sentencia batch sobre el ejecutor (pgx acepta múltiples statements en un Exec).
func aplicarArchivo(ctx context.Context, ej Ejecutor, e Esquema, nombre string) error {
	ruta := e.Dir + "/" + nombre
	sqlBytes, err := fs.ReadFile(e.FS, ruta)
	if err != nil {
		return fmt.Errorf("migrate: leer %q del esquema %q: %w", ruta, e.Nombre, err)
	}
	if _, err := ej.Exec(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("migrate: aplicar %q (rol %q): %w", ruta, e.Rol, err)
	}
	return nil
}

// archivosDe devuelve los .sql de un directorio del FS, ordenados por nombre
// (0001 antes que 0002). El orden por nombre es la fuente del orden de aplicación
// dentro de un esquema.
func archivosDe(fsys fs.FS, dir string) ([]string, error) {
	entradas, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: leer directorio %q: %w", dir, err)
	}
	nombres := make([]string, 0, len(entradas))
	for _, e := range entradas {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			nombres = append(nombres, e.Name())
		}
	}
	sort.Strings(nombres)
	return nombres, nil
}
