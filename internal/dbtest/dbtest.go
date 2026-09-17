// Package dbtest ofrece utilidades para las pruebas de integración que
// requieren Postgres. Todas las pruebas que lo usan se saltan (t.Skip) cuando
// la variable PLATFORM_GO_TEST_DB no está definida, de modo que `go test ./...`
// sigue pasando en un entorno sin base de datos (p. ej. CI sin servicio de BD).
//
// PLATFORM_GO_TEST_DB debe contener un DSN de Postgres con privilegios de
// superusuario o, al menos, capacidad de CREATE ROLE y CREATE SCHEMA, porque
// las pruebas de aislamiento por schema crean roles y schemas efímeros.
//
// Ejemplo:
//
//	PLATFORM_GO_TEST_DB="postgres:///postgres" go test ./...
package dbtest

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvDSN es la variable de entorno que activa las pruebas de integración de BD.
const EnvDSN = "PLATFORM_GO_TEST_DB"

// DSN devuelve el DSN de administración, o salta la prueba si no está definido.
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Skipf("se salta la prueba de integración: %s no está definido", EnvDSN)
	}
	return dsn
}

// AdminConn abre una conexión de administración (para crear/eliminar roles y
// schemas). El llamador la cierra con la función devuelta.
func AdminConn(t *testing.T, ctx context.Context) (*pgx.Conn, func()) {
	t.Helper()
	conn, err := pgx.Connect(ctx, DSN(t))
	if err != nil {
		t.Fatalf("dbtest: conectar como admin: %v", err)
	}
	return conn, func() { _ = conn.Close(ctx) }
}

// Exec ejecuta una sentencia con la conexión de administración y falla la
// prueba si hay error. Útil para el setup/teardown de roles y schemas.
func Exec(t *testing.T, ctx context.Context, conn *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("dbtest: exec %q: %v", sql, err)
	}
}

// PoolAs abre un pgxpool usando un rol específico sobre el mismo servidor que
// el DSN de administración, cambiando el usuario. Devuelve el pool y su cierre.
// Se apoya en que el DSN admin permita `SET ROLE` o en credenciales del rol;
// aquí usamos SET ROLE tras conectar, que es suficiente para probar los GRANT.
func PoolAs(t *testing.T, ctx context.Context, role string) (*pgxpool.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(DSN(t))
	if err != nil {
		t.Fatalf("dbtest: parsear DSN: %v", err)
	}
	// Cada conexión del pool asume el rol de prueba, de modo que los privilegios
	// efectivos sean los del rol y no los del superusuario que abrió el pool.
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+quoteIdent(role))
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("dbtest: crear pool como %s: %v", role, err)
	}
	return pool, pool.Close
}

// quoteIdent escapa un identificador SQL de forma mínima (doble comilla).
func quoteIdent(ident string) string {
	out := make([]byte, 0, len(ident)+2)
	out = append(out, '"')
	for i := 0; i < len(ident); i++ {
		if ident[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, ident[i])
	}
	out = append(out, '"')
	return string(out)
}
