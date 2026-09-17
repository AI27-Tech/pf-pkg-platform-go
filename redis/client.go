// Package redis ofrece un cliente Redis compartido para estado vivo, pub/sub
// y sesión. Cada servicio importa este paquete y usa el cliente devuelto.
package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

// Client envuelve el cliente go-redis con un health check.
type Client struct {
	*goredis.Client
}

// New crea un nuevo cliente Redis desde una URL de conexión.
// La URL debe estar en el formato "redis://host:port/db" o "rediss://host:port/db".
func New(ctx context.Context, url string) (*Client, error) {
	if url == "" {
		return nil, fmt.Errorf("redis: la URL no debe estar vacía")
	}

	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: parsear URL: %w", err)
	}

	// Valores por defecto razonables para un cliente compartido.
	if opts.PoolSize == 0 {
		opts.PoolSize = 10
	}
	if opts.MinIdleConns == 0 {
		opts.MinIdleConns = 2
	}

	client := goredis.NewClient(opts)

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}

	return &Client{Client: client}, nil
}

// CheckFunc devuelve una función de health check adecuada para el registro de health.
func (c *Client) CheckFunc() func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return c.Ping(ctx).Err()
	}
}
