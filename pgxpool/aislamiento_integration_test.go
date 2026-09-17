package pgxpool_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/internal/dbtest"
)

// TestAislamientoPorSchema verifica el invariante I1 de ADR 17 / criterio E0-T1:
// el rol de un módulo NO tiene GRANT sobre el schema de otro módulo, de modo que
// un SELECT cruzado falla con "permission denied for schema".
//
// Se salta si PLATFORM_GO_TEST_DB no está definido.
func TestAislamientoPorSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()

	// Nombres efímeros para no colisionar con datos reales.
	suf := fmt.Sprintf("_t%d", time.Now().UnixNano())
	schemaA := "modulo_a" + suf
	schemaB := "modulo_b" + suf
	roleA := "rol_a" + suf
	roleB := "rol_b" + suf

	// Teardown primero por si una corrida previa quedó a medias.
	teardown := func() {
		for _, s := range []string{schemaA, schemaB} {
			dbtest.Exec(t, ctx, admin, fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, s))
		}
		for _, r := range []string{roleA, roleB} {
			dbtest.Exec(t, ctx, admin, fmt.Sprintf(`DROP ROLE IF EXISTS %q`, r))
		}
	}
	teardown()
	defer teardown()

	// Setup: dos schemas, dos roles; cada rol con USAGE solo sobre su schema.
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`CREATE ROLE %q LOGIN`, roleA))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`CREATE ROLE %q LOGIN`, roleB))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`CREATE SCHEMA %q`, schemaA))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`CREATE SCHEMA %q`, schemaB))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`CREATE TABLE %q.dato (id int)`, schemaA))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`CREATE TABLE %q.dato (id int)`, schemaB))

	// El punto del test: cada rol SOLO recibe USAGE/SELECT sobre su propio schema.
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`GRANT USAGE ON SCHEMA %q TO %q`, schemaA, roleA))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`GRANT SELECT ON %q.dato TO %q`, schemaA, roleA))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`GRANT USAGE ON SCHEMA %q TO %q`, schemaB, roleB))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf(`GRANT SELECT ON %q.dato TO %q`, schemaB, roleB))

	// El rol A puede leer su propio schema.
	poolA, closeA := dbtest.PoolAs(t, ctx, roleA)
	defer closeA()
	if _, err := poolA.Exec(ctx, fmt.Sprintf(`SELECT 1 FROM %q.dato`, schemaA)); err != nil {
		t.Fatalf("rol A debería poder leer su propio schema, pero falló: %v", err)
	}

	// El punto: el rol A NO puede leer el schema de B.
	_, err := poolA.Exec(ctx, fmt.Sprintf(`SELECT 1 FROM %q.dato`, schemaB))
	if err == nil {
		t.Fatalf("SELECT cruzado (rol A -> schema B) debería fallar, pero tuvo éxito")
	}
	if !strings.Contains(err.Error(), "permission denied for schema") {
		t.Fatalf("se esperaba 'permission denied for schema', se obtuvo: %v", err)
	}
}
