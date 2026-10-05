# CHANGELOG

## 2026:10:05 00:00

**Resumen:** Deltas P1–P9 de `01-cimientos-compartidos.md` sobre `pf-pkg-platform-go`: se agregaron las piezas reutilizables de workers y bus de dominio (puerto agnóstico + pruebas de contrato + adaptador en memoria), inbox, outbox.Relay, métricas OTel y helpers de traza, el refactor de `auth` (Audiencia de dominio separada del claim técnico `aud`, Authenticator, middleware común con forma de Error, refresco proactivo del JWKS), la herramienta `arch-test`, la plantilla Docker parametrizada y la CI. El adaptador concreto del broker V1 queda **en espera de ADR 32** (on-premise; propuesta NATS JetStream) y no se construye aquí.

**Cambios:**
- `worker/`: interfaz `Worker` (Name/Run/Ready) y `RunGroup`, que arranca N workers, registra su readiness en `health.Registry` y espera el drenado al apagar; apagado coordinado si uno falla.
- `bus/`: puerto agnóstico del bus de dominio (ADR 29) — `Sobre`, `Publisher`, `Subscriber`, `Handler`, `Registry` (lo inyecta el módulo) y `Broadcaster`. Sin transporte, sin dominio. `bus/bustest`: pruebas de contrato (orden por clave de agregado, venenoso→cuarentena antes del acuse, duplicado→un solo efecto, acuse tras commit, separación amplia/restringida, difusión a todas las réplicas). `bus/memory`: adaptador en memoria que pasa `bustest`; solo desarrollo y pruebas.
- `inbox/`: `InboxDDL` + `Registrar` idempotente por `evento_id` en la tx del efecto; `ErrYaProcesado` distingue duplicado de fallo (ADR 29 §1).
- `outbox/relay.go`: `Relay` (worker) que drena la outbox del módulo y publica por `bus.Publisher`, marcando `publicado` solo tras el acuse; un fallo de transporte deja la fila pendiente (at-least-once) y detiene el barrido para preservar el orden por agregado.
- `observability/`: `InitMeterProvider` y `Init` (trazas + métricas con shutdown combinado); `WorkerMetrics` (procesados, fallidos, en cuarentena, pendientes, antigüedad, duración); helpers `Inject`/`Extract` y `IniciarSpanConsumidor`.
- `auth/`: se separó `Audience` (claim técnico `aud`) de `Audiencia` (dominio `Cliente`|`Interno`, DEC-054, con derivación desde el rol como respaldo); interfaz `Authenticator`, función `Middleware(Authenticator, RevocationChecker)` con la forma `Error` de la plataforma (I3), `ContextWithClaims` exportado, costura opcional de revocación (ADR 26 §3) y refresco proactivo del JWKS antes del TTL con jitter, conservando la gracia de 30 s como respaldo (DEC-056). El verificador no conoce al proveedor: URL y emisor se inyectan.
- `tools/arch-test/`: `main` + `reglas.yaml` embebido (única copia del grafo, ADR 17/E0-T6) + verificación de las reglas (a)–(e) con `go list -deps`, detección de ciclos y casos negativos en pruebas.
- `templates/Dockerfile`: plantilla parametrizada por `CMD_PATH` (obligatorio, sin default); se quitaron `ROOT`/`MODULE_PATH`; token privado como secreto de BuildKit. Se eliminó el `Dockerfile` de la raíz.
- `.github/workflows/ci.yml`: build, vet, test (`-race`) con servicio Postgres y `PLATFORM_GO_TEST_DB`, pruebas de `arch-test` (incluidos negativos) y build de `templates/Dockerfile` contra `cmd/ejemplo/api`.
- Correcciones de prosa/comentarios a la realidad on-premise (ADR 29/32): se quitaron referencias a Cognito, SNS FIFO y `MessageGroupId` del README, `outbox/publisher.go`, `config/config.go` y el comentario de `cmd/ejemplo/api/main.go`.
- Dependencias: `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp`, `go.opentelemetry.io/otel/sdk/metric` y `gopkg.in/yaml.v3` como directas.

**Pendiente (reportado, no bloquea este entregable):**
- `bus/<broker>/` (adaptador del broker V1) y la difusión real de `sesion.revocada` esperan a que ADR 32 elija componente (DEC-062). Aquí solo van el puerto y el doble en memoria.
- La proyección compartida de revocación (E0-T4) usa la costura de P8; dónde vive queda para 03 y 09.

**Archivos modificados:**
- `worker/worker.go`, `worker/worker_test.go`
- `bus/bus.go`, `bus/bustest/bustest.go`, `bus/memory/memory.go`, `bus/memory/memory_test.go`
- `inbox/inbox.go`
- `outbox/relay.go`, `outbox/relay_test.go`, `outbox/publisher.go`
- `observability/metrics.go`, `observability/trace.go`
- `auth/jwt.go`, `auth/jwt_test.go`
- `tools/arch-test/main.go`, `tools/arch-test/reglas.go`, `tools/arch-test/reglas.yaml`, `tools/arch-test/verificar.go`, `tools/arch-test/verificar_test.go`
- `templates/Dockerfile` (nuevo), `Dockerfile` (eliminado)
- `.github/workflows/ci.yml`
- `cmd/ejemplo/api/main.go`, `config/config.go`
- `README.md`, `go.mod`, `go.sum`

## 2026:09:17 11:16

**Resumen:** Se terminó `platform-go` a la especificación E0-T1: se alineó la outbox con el sobre de contratos, se completó la propagación de OpenTelemetry hasta el evento, se agregó una plantilla `cmd/` copiable y se cubrió la Definición de Terminado con pruebas (aislamiento por schema, supervivencia de outbox, traza HTTP → outbox) más pruebas del verificador JWT.

**Cambios:**
- Outbox alineada con el `Sobre` de `pf-pkg-contracts`: `Event` ahora lleva `EventoID` (UUID autogenerado si falta), `Tipo`, `Version`, `AgregadoID` (clave de orden, obligatoria), `Sensibilidad` (amplia/restringida, default amplia), `OcurridoEn` (UTC) y `Payload`. Se agregó `outbox.OutboxDDL` con la tabla estándar (`evento_id` único, `trace_context` JSONB, `publicado`, índice parcial de pendientes). Se eliminó `PublishWithMetadata` (el sobre cubre la necesidad).
- Propagación OpenTelemetry completa: `observability.InitTracerProvider` fija el propagador W3C global (`TraceContext`+`Baggage`); el servidor instrumenta el mux con `otelhttp` para iniciar span por petición; el `Publisher` persiste el contexto de traza del `ctx` en la columna `trace_context` y expone `TraceContextFromRow`/`SpanContextFromRow` para el worker.
- Plantilla `cmd/ejemplo/api/main.go`: raíz de composición mínima copiable (config → observabilidad → servidor con `/health` y `/ready` → apagado ordenado).
- Corrección de `config.Load`: las URLs de Postgres/Redis/JWKS/OTel pasan a ser **opcionales** en el `Config` base; cada módulo declara las obligatorias vía `requiredEnvVars`. Antes eran obligatorias siempre, lo que impedía arrancar un módulo mínimo.
- Pruebas: `TestAislamientoPorSchema` (verifica `permission denied for schema`), `TestOutboxSobreviveReinicio`, `TestTraceRoundTrip`/`TestTraceSinSpan` y `TestTrazaHTTPHastaOutbox` (traza HTTP → outbox), y 11 pruebas del verificador JWT (`auth/jwt_test.go`: firma válida/ inválida, expirado, audiencia, `kid`, fail-closed, gracia, middleware, claims). Las de integración se saltan sin `PLATFORM_GO_TEST_DB`.
- Helper de pruebas `internal/dbtest` para las pruebas que requieren Postgres.
- Se corrigió un comentario corrupto en `auth/jwt.go` y se tradujeron comentarios de `auth/helpers.go`. README ampliado (outbox, pruebas de integración) y `/readiness` → `/ready`.
- Dependencias: se agregaron `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` y `github.com/google/uuid` como directas; se mantuvo todo OpenTelemetry alineado en v1.46.

**Archivos modificados:**
- `outbox/publisher.go`, `outbox/otel.go`
- `observability/otel.go`
- `server/server.go`
- `config/config.go`, `config/config_test.go`
- `auth/jwt.go`, `auth/helpers.go`, `auth/jwt_test.go`
- `cmd/ejemplo/api/main.go`
- `internal/dbtest/dbtest.go`
- `pgxpool/aislamiento_integration_test.go`
- `outbox/publisher_integration_test.go`, `outbox/trace_test.go`, `outbox/trace_http_integration_test.go`
- `README.md`
- `go.mod`, `go.sum`

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
