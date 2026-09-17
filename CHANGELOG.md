# CHANGELOG

## 2026:09:17 12:30

**Resumen:** Corrección de comentarios, docstrings y mensajes de error a español (México) para cumplir con `naming-conventions.md`.

**Cambios:**
- Se tradujeron todos los comentarios y docstrings de código de inglés a español.
- Se tradujeron los mensajes de error de los paquetes a español.
- Se tradujeron los mensajes de error HTTP (auth middleware) a español.
- Se actualizaron los tests unitarios con descripciones en español.
- Se mantuvieron los identificadores de código en inglés (excepto sustantivos de dominio como `Audiencia`, `SesionID`, `Rol`).

**Archivos modificados:**
- `config/config.go`
- `config/config_test.go`
- `errors/errors.go`
- `errors/errors_test.go`
- `health/health.go`
- `health/health_test.go`
- `pgxpool/pool.go`
- `outbox/publisher.go`
- `auth/jwt.go`
- `observability/otel.go`
- `redis/client.go`
- `server/server.go`

## 2026:09:17 12:00

**Resumen:** Creación del entregable cero E0-T1: paquete compartido `packages/platform-go` con los componentes fundamentales para los microservicios de AI27 V1.

**Cambios:**
- Se creó el módulo Go `github.com/AI27-Tech/pf-pkg-platform-go` con Go 1.27.
- Se implementó `config/`: carga de variables de entorno con validación fail-fast y helpers para int, duration, bool.
- Se implementó `errors/`: taxonomía estable de errores (no_autorizado, no_encontrado, validacion, conflicto, degradado, roto, rate_limit) con mapeo a HTTP status.
- Se implementó `health/`: handlers HTTP `/health` (liveness) y `/ready` (readiness) con registry de checks por servicio.
- Se implementó `pgxpool/`: wrapper de pool de Postgres por rol de BD, con health check para readiness (invariante I1).
- Se implementó `outbox/`: publisher transaccional que inserta eventos en la tabla outbox del schema del llamador dentro de la misma transacción.
- Se implementó `auth/`: verificador JWT con JWKS cacheado, TTL, fallo cerrado, extracción de claims (rol, tenant, Audiencia, sesion_id) y middleware HTTP.
- Se implementó `observability/`: inicialización de OpenTelemetry con exportador OTLP HTTP y resource attributes.
- Se implementó `redis/`: cliente Redis compartido con health check para readiness.
- Se implementó `server/`: servidor HTTP con graceful shutdown, signal handling, drenado de conexiones y health routes.
- Se creó `Dockerfile` multi-stage parametrizado por raíz de composición.
- Se creó `README.md` con documentación completa del paquete.
- Se agregaron tests unitarios para config (4 tests), errors (5 tests) y health (6 tests).

**Archivos modificados:**
- `go.mod`
- `config/config.go`
- `config/config_test.go`
- `errors/errors.go`
- `errors/errors_test.go`
- `health/health.go`
- `health/health_test.go`
- `pgxpool/pool.go`
- `outbox/publisher.go`
- `auth/jwt.go`
- `auth/helpers.go`
- `observability/otel.go`
- `redis/client.go`
- `server/server.go`
- `Dockerfile`
- `README.md`
