// Package config ofrece carga de configuración desde variables de entorno
// con validación al arrancar (fail-fast si falta una variable requerida).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Claves de variables de entorno usadas por platform-go.
const (
	EnvHTTPAddr        = "HTTP_ADDR"
	EnvDatabaseURL     = "DATABASE_URL"
	EnvRedisURL        = "REDIS_URL"
	EnvJWKSURL         = "JWKS_URL"
	EnvOtelExporterURL = "OTEL_EXPORTER_URL"
	EnvServiceName     = "SERVICE_NAME"
)

// Config contiene la configuración validada de un servicio.
// Todos los campos se llenan desde variables de entorno.
type Config struct {
	// HTTPAddr es la dirección donde escucha el servidor HTTP (ej. ":8080").
	HTTPAddr string
	// DatabaseURL es la cadena de conexión de Postgres para el rol principal.
	DatabaseURL string
	// RedisURL es la cadena de conexión de Redis (ej. "redis://localhost:6379").
	RedisURL string
	// JWKSURL es la URL para obtener el JWKS del IdP interno y verificar JWT.
	JWKSURL string
	// OtelExporterURL es el endpoint del exportador OTLP HTTP (ej. "http://localhost:4318").
	OtelExporterURL string
	// ServiceName es el nombre lógico del servicio (usado en trazas/métricas).
	ServiceName string
}

// Load lee las variables de entorno y devuelve un Config validado.
//
// Filosofía: el sustrato no impone dependencias. Sólo `SERVICE_NAME` y
// `HTTP_ADDR` tienen valor por defecto y siempre están presentes; las URLs de
// Postgres, Redis, JWKS y OTel son OPCIONALES en el Config base, porque no todo
// módulo las usa (p. ej. `catalogos` no usa Redis ni outbox). Cada módulo
// declara las que SÍ necesita pasándolas en requiredEnvVars, y entonces su
// ausencia es un error de arranque.
//
// Devuelve un error con TODAS las variables faltantes, no sólo la primera.
func Load(requiredEnvVars ...string) (*Config, error) {
	var missing []string

	cfg := &Config{
		HTTPAddr:        getOrDefault(EnvHTTPAddr, ":8080"),
		DatabaseURL:     os.Getenv(EnvDatabaseURL),
		RedisURL:        os.Getenv(EnvRedisURL),
		JWKSURL:         os.Getenv(EnvJWKSURL),
		OtelExporterURL: os.Getenv(EnvOtelExporterURL),
		ServiceName:     getOrDefault(EnvServiceName, "unknown"),
	}

	// El módulo declara qué variables son obligatorias para él.
	for _, key := range requiredEnvVars {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("config: faltan variables de entorno requeridas: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// getOrDefault devuelve el valor de la env var o el fallback si está vacía.
func getOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetEnvInt lee una variable de entorno como entero.
// Devuelve el valor por defecto si la variable está vacía o no es un entero válido.
func GetEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// GetEnvDuration lee una variable de entorno como time.Duration.
// Soporta formato de duración Go (ej. "5s", "1m30s").
// Devuelve el valor por defecto si la variable está vacía o no es una duración válida.
func GetEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

// GetEnvBool lee una variable de entorno como booleano.
// Devuelve el valor por defecto si la variable está vacía o no es un booleano válido.
func GetEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
