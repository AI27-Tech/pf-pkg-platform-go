package config

import (
	"os"
	"testing"
)

func TestLoad_Exitoso(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgres://localhost/test")
	os.Setenv("SERVICE_NAME", "test-service")
	os.Setenv("REDIS_URL", "redis://localhost:6379")
	os.Setenv("JWKS_URL", "https://cognito.example.com/.well-known/jwks.json")
	os.Setenv("OTEL_EXPORTER_URL", "http://localhost:4318")
	defer os.Unsetenv("DATABASE_URL")
	defer os.Unsetenv("SERVICE_NAME")
	defer os.Unsetenv("REDIS_URL")
	defer os.Unsetenv("JWKS_URL")
	defer os.Unsetenv("OTEL_EXPORTER_URL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	if cfg.DatabaseURL != "postgres://localhost/test" {
		t.Errorf("DatabaseURL = %q, se esperaba %q", cfg.DatabaseURL, "postgres://localhost/test")
	}
	if cfg.ServiceName != "test-service" {
		t.Errorf("ServiceName = %q, se esperaba %q", cfg.ServiceName, "test-service")
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, se esperaba %q", cfg.HTTPAddr, ":8080")
	}
}

func TestLoad_FaltanRequeridas(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("SERVICE_NAME")
	os.Unsetenv("REDIS_URL")
	os.Unsetenv("JWKS_URL")
	os.Unsetenv("OTEL_EXPORTER_URL")

	_, err := Load("DATABASE_URL", "SERVICE_NAME")
	if err == nil {
		t.Fatal("se esperaba error por variables de entorno faltantes")
	}
}

func TestLoad_ValoresPorDefecto(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgres://localhost/test")
	os.Setenv("SERVICE_NAME", "test-service")
	os.Setenv("REDIS_URL", "redis://localhost:6379")
	os.Setenv("JWKS_URL", "https://cognito.example.com/.well-known/jwks.json")
	os.Setenv("OTEL_EXPORTER_URL", "http://localhost:4318")
	os.Unsetenv("HTTP_ADDR")
	defer os.Unsetenv("DATABASE_URL")
	defer os.Unsetenv("SERVICE_NAME")
	defer os.Unsetenv("REDIS_URL")
	defer os.Unsetenv("JWKS_URL")
	defer os.Unsetenv("OTEL_EXPORTER_URL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, se esperaba el valor por defecto %q", cfg.HTTPAddr, ":8080")
	}
}

func TestGetEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")

	if got := GetEnvInt("TEST_INT", 0); got != 42 {
		t.Errorf("GetEnvInt = %d, se esperaba 42", got)
	}

	if got := GetEnvInt("MISSING_INT", 7); got != 7 {
		t.Errorf("GetEnvInt fallback = %d, se esperaba 7", got)
	}

	os.Setenv("TEST_INT_BAD", "notanumber")
	defer os.Unsetenv("TEST_INT_BAD")
	if got := GetEnvInt("TEST_INT_BAD", 5); got != 5 {
		t.Errorf("GetEnvInt malo = %d, se esperaba 5 (fallback)", got)
	}
}
