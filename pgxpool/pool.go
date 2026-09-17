// Package pgxpool ofrece la creación de pools de Postgres por rol de base de datos.
// Cada módulo obtiene su propio pool con su propio rol; nunca se comparte un pool
// entre roles (invariante I1 de ADR 17).
package pgxpool

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool envuelve un pgxpool.Pool con el nombre de su rol para diagnósticos.
type Pool struct {
	*pgxpool.Pool
	Role string
}

// New crea un nuevo pool de conexiones para el rol de BD dado.
// La connString debe incluir las credenciales del rol y la base de datos destino.
// El pool valida la conectividad antes de devolver.
func New(ctx context.Context, role, connString string) (*Pool, error) {
	if role == "" {
		return nil, fmt.Errorf("pgxpool: el rol no debe estar vacío")
	}
	if connString == "" {
		return nil, fmt.Errorf("pgxpool: la cadena de conexión no debe estar vacía para el rol %q", role)
	}

	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: parsear configuración para rol %q: %w", role, err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: crear pool para rol %q: %w", role, err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgxpool: ping rol %q: %w", role, err)
	}

	return &Pool{Pool: pool, Role: role}, nil
}

// CheckFunc devuelve una función de health check adecuada para el registro de health.
// Hace ping al pool y devuelve un error si el pool no está saludable.
func (p *Pool) CheckFunc() func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return p.Ping(ctx)
	}
}
