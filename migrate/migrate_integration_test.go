package migrate_test

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/internal/dbtest"
	"github.com/AI27-Tech/pf-pkg-platform-go/migrate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// connEjecutor adapta una *pgx.Conn a migrate.Ejecutor (Exec coincide en firma).
type connEjecutor struct{ c *pgx.Conn }

func (e connEjecutor) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return e.c.Exec(ctx, sql, args...)
}

// TestAplicarContraPostgres ejerce el aplicador contra Postgres real con SQL que
// reproduce el patrón que obliga las dos fases: el esquema `b` otorga, en su
// fase 2 (grants), un privilegio al rol que crea el esquema `a` en su fase 1. Si
// el aplicador corriera esquema por esquema de principio a fin, el grant de `b`
// correría antes de existir el rol de `a` y fallaría. Las dos fases lo evitan.
//
// Se salta si PLATFORM_GO_TEST_DB no está definido. Usa nombres efímeros y limpia
// al entrar y salir. La conexión admin aplica TODO (credencial administrativa del
// bootstrap, DEC-096): el aplicador no abre conexiones, solo recibe ejecutores.
func TestAplicarContraPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, cerrar := dbtest.AdminConn(t, ctx) // hace t.Skip si no hay DSN
	defer cerrar()

	limpiar := func() {
		dbtest.Exec(t, ctx, admin, "DROP SCHEMA IF EXISTS mig_a CASCADE")
		dbtest.Exec(t, ctx, admin, "DROP SCHEMA IF EXISTS mig_b CASCADE")
		dbtest.Exec(t, ctx, admin, "DROP ROLE IF EXISTS mig_rol_a")
		dbtest.Exec(t, ctx, admin, "DROP ROLE IF EXISTS mig_rol_b")
	}
	limpiar()
	defer limpiar()

	// FS de prueba: dos esquemas, `a` antes de `b` en el módulo. El grant cruzado
	// vive en `mig_b/0002` y referencia `mig_rol_a`, que crea `mig_a/0001`. Con
	// las dos fases, la fase 1 corre los dos 0001 (crea ambos roles/esquemas) y la
	// fase 2 corre los grants, así que el grant cruzado de `b` ya encuentra el rol
	// de `a`. Es el mismo patrón que la excepción de GRANT de operacion.
	fs := fstest.MapFS{
		"mig_a/0001_init.sql": {Data: []byte(`
			CREATE SCHEMA IF NOT EXISTS mig_a;
			DO $$ BEGIN
			  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='mig_rol_a') THEN
			    CREATE ROLE mig_rol_a NOLOGIN;
			  END IF;
			END $$;
			CREATE TABLE IF NOT EXISTS mig_a.cosa (id int primary key);
		`)},
		"mig_a/0002_grants.sql": {Data: []byte(`
			GRANT USAGE ON SCHEMA mig_a TO mig_rol_a;
		`)},
		"mig_b/0001_init.sql": {Data: []byte(`
			CREATE SCHEMA IF NOT EXISTS mig_b;
			DO $$ BEGIN
			  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='mig_rol_b') THEN
			    CREATE ROLE mig_rol_b NOLOGIN;
			  END IF;
			END $$;
			CREATE TABLE IF NOT EXISTS mig_b.otra (id int primary key);
		`)},
		"mig_b/0002_grants.sql": {Data: []byte(`
			-- grant CRUZADO: mig_b otorga a mig_rol_a, creado por mig_a/0001.
			-- Solo funciona si la fase 1 (0001 de ambos) ya corrió.
			GRANT USAGE ON SCHEMA mig_b TO mig_rol_a;
		`)},
	}

	m := migrate.Modulo{
		Nombre: "prueba",
		Esquemas: []migrate.Esquema{
			{Nombre: "mig_a", Rol: "mig_rol_a", FS: fs, Dir: "mig_a"},
			{Nombre: "mig_b", Rol: "mig_rol_b", FS: fs, Dir: "mig_b"},
		},
	}

	// Todo se aplica con la conexión admin (credencial de bootstrap).
	resolver := func(rol string) (migrate.Ejecutor, error) {
		return connEjecutor{c: admin}, nil
	}

	if err := migrate.Aplicar(ctx, m, resolver); err != nil {
		t.Fatalf("Aplicar contra Postgres falló: %v", err)
	}

	// Verificar el efecto real: las dos tablas existen y el grant cruzado quedó.
	var existeTabla bool
	if err := admin.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='mig_b' AND table_name='otra')`,
	).Scan(&existeTabla); err != nil {
		t.Fatalf("consultar tabla mig_b.otra: %v", err)
	}
	if !existeTabla {
		t.Error("mig_b.otra no se creó")
	}

	var tieneGrant bool
	if err := admin.QueryRow(ctx,
		`SELECT has_schema_privilege('mig_rol_a', 'mig_b', 'USAGE')`,
	).Scan(&tieneGrant); err != nil {
		t.Fatalf("consultar grant cruzado: %v", err)
	}
	if !tieneGrant {
		t.Error("el grant cruzado mig_b -> mig_rol_a no se aplicó (¿fallaron las dos fases?)")
	}

	// Idempotencia: reaplicar no debe fallar (los .sql usan IF NOT EXISTS).
	if err := migrate.Aplicar(ctx, m, resolver); err != nil {
		t.Fatalf("reaplicar debió ser idempotente, falló: %v", err)
	}
}
