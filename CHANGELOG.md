# CHANGELOG

## 2026-10-30 — errores: categoría Prohibido (403) y código de máquina en Conflicto (Ola C, E8-T14)

**Resumen:** la taxonomía gana una categoría `CategoryProhibido` (mapea a 403, `String()` = `"prohibido"`) para el caso "autenticado pero sin facultad", distinto de `NoAutorizado` (401, sin autenticar). Y `PlatformError` gana un campo opcional `Codigo`: un código de máquina más fino que `Category.String()`, para que un conflicto que el cliente necesita distinguir (p. ej. `cierre_anticipado_solo_por_incidencia`) viaje como código estable sin crear una categoría por cada variante de 409.

**Contexto:** antes solo existía `NoAutorizado` (que aplastaba 403 en 401) y el 409 salía siempre con código genérico `"conflicto"`; el detalle quedaba en el mensaje libre. El `bff` tenía un tipo `Prohibido` local para fabricar el 403 en su borde, y `mensajeria` metía cadenas-código en el mensaje del conflicto. Esta ola da a la plataforma la pieza que ambos imitaban. Resuelve A3/B1 de E8-T14 (DEC-111).

**Aditivo, sin regresión:** `CategoryProhibido` se añade al final del enum (no renumera los iota existentes). El campo `Codigo` es opcional: cuando está vacío, el borde HTTP sigue usando `Category.String()` como hoy. Ningún servicio cambia de comportamiento hasta que adopte los constructores nuevos.

**API nueva:**
- `CategoryProhibido` + `NewProhibido(msg, args...)` → 403 / `"prohibido"`.
- `NewConflictoCodigo(codigo, msg, args...)` → 409 con `Codigo` específico.
- `CodigoDe(err) string`: el código que el borde debe emitir (el `Codigo` del error si lo trae, si no `Category.String()`, y `"desconocido"` para un error ajeno). Centraliza la regla para que cada responder no la repita.

**Verificación:** `gofmt`, `go vet`, `go test ./errors/` en verde. Tests nuevos para 403, `NewConflictoCodigo`, `NewProhibido` y `CodigoDe` (tres ramas).

## 2026:10:19 (hora local) — paquete `migrate`: aplicador de migraciones en dos fases (E0-T7, DEC-096)

**Resumen:** Nuevo paquete `migrate` que aplica las migraciones SQL que cada módulo embebe (`migrations.FS`), respetando el orden entre esquemas y la separación en **dos fases** que la excepción de GRANT de `operacion` obliga (ADR 33): primero el `0001` de cada esquema (estructura: esquema, rol, tablas), luego el resto de cada esquema (grants cruzados y migraciones posteriores). Es la pieza que implementa DEC-096 por el lado de plataforma; no abre conexiones ni elige credenciales.

**Contexto:** los `cmd/` de core y mensajeria tenían un TODO de "aplicar migraciones antes de servir tráfico", pero los `0001` hacen `CREATE SCHEMA`/`CREATE ROLE`, que un rol de app con privilegio mínimo no puede. DEC-096 decidió que esas migraciones de estructura corran como **bootstrap de despliegue** con credencial administrativa, no desde el arranque del servicio (invariante I1). Por eso el aplicador recibe un `ResolverEjecutor` (rol → `Ejecutor`) y nunca abre conexiones: el bootstrap le pasa una conexión administrativa; el servicio nunca porta privilegio de crear schema/rol. Es el mismo trabajo que hoy hace `scripts/gate-g2/entorno.sh` en bash, promovido a un artefacto reutilizable en Go.

**Cambios:**
- `migrate/migrate.go` (nuevo): tipos `Esquema`, `Modulo`, `Ejecutor`, `ResolverEjecutor` y `Aplicar(ctx, Modulo, ResolverEjecutor)`. `Aplicar` valida por adelantado (archivos presentes, ejecutor por rol) y falla antes de tocar la base si algo falta; luego aplica en dos fases. Sin dependencia externa nueva (solo pgx, ya presente).
- `migrate/migrate_test.go` (nuevo): orden de dos fases con un módulo multi-esquema, caso de un solo esquema, validación previa que no aplica nada al fallar, rol sin ejecutor, y propagación de error de `Exec`. Con FS falso (`testing/fstest`) y un ejecutor espía; sin Postgres.
- `migrate/migrate_integration_test.go` (nuevo): aplica contra Postgres real SQL que reproduce el grant CRUZADO (esquema `b/0002` otorga al rol que crea `a/0001`), verifica que el grant quedó y que reaplicar es idempotente. Guardado por `PLATFORM_GO_TEST_DB` (se salta sin DSN).

**Verificado:** `gofmt` limpio, `go vet ./...`, `go build ./...`, `go test ./...` (suite completa del repo en verde). Test de integración en verde contra Postgres 16 local (`PLATFORM_GO_TEST_DB`). `arch-test` del repo en verde.

**Fuera de este cambio (pasos siguientes de E0-T7, en otros repos):** cablear `migrate.Aplicar` como paso de bootstrap de despliegue, retirar los TODO de migraciones de `cmd/core`/`cmd/mensajeria` y de los `cmd/api` standalone, y reusarlo desde el gate. Esos tocan pf-svc-core/pf-svc-mensajeria y consumen una versión publicada de este paquete.

## 2026:10:14 (hora local) — nombre de worker durable consistente entre transportes

**Resumen:** El consumidor durable del bus en memoria ahora nombra su worker con el mismo esquema que el de NATS: `bus-durable:<durableName>`. Antes el bus en memoria devolvía solo `<durableName>`, así que un mismo módulo (p. ej. reportes) aparecía en readiness como `worker:reportes` con el bus en memoria y como `worker:bus-durable:reportes` con NATS. Esa discrepancia rompía la paridad de observabilidad y obligaba a los tests a sortear el nombre.

**Cambios:**
- `bus/memory/memory.go`: `consumidor.Name()` pasa de `c.nombre` a `"bus-durable:" + c.nombre`, igual que `bus/nats/jetstream.go`. Comentario que ata ambos: si cambia en uno, cambia en el otro.
- `bus/bustest/bustest.go`: nuevo caso de contrato `NombreDurableConPrefijo` en la batería compartida (`Ejecutar`), que afirma `Subscribe("mod-a", …).Name() == "bus-durable:mod-a"`. Al vivir en `bustest`, el contrato se verifica en AMBOS transportes (memoria y NATS) con una sola aserción.

**Verificado:** gofmt limpio, `go vet ./...`, `go build ./...`, `go test ./...` (batería de memoria con el caso nuevo en verde). Batería de NATS con el caso nuevo en verde contra un broker local (`go test -tags nats ./bus/nats/`, NATS 2 por docker). CI sin broker sigue en verde (el paquete `bus/nats` no corre sin el tag).

**Archivos modificados:**
- `bus/memory/memory.go`
- `bus/bustest/bustest.go`

## 2026:10:05 (hora local)

**Resumen:** Se corrige la compuerta muerta de acceso a módulos privados en el CI. El paso "Configurar acceso a módulos privados" tenía `if: ${{ env.GH_PAT != '' }}`, que se evalúa ANTES de aplicar el `env` del propio paso, así que `env.GH_PAT` siempre estaba vacío y el paso se saltaba siempre. En platform-go era inofensivo (no tiene dependencias privadas), pero era configuración muerta y engañosa, inconsistente con los repos `pf-svc-*`.

**Cambios:**
- `.github/workflows/ci.yml`: `GH_PAT` y `GOPRIVATE` suben a nivel de job en `build-test`; el paso de configuración de git deja de tener el `if` muerto y corre incondicionalmente (si `GH_PAT` está vacío, el `insteadOf` no estorba a los módulos públicos). Mismo patrón ya aplicado en core/catalogos/etc.

**Verificado:** YAML válido. No cambia el comportamiento observable del CI de platform-go (no tiene dependencias privadas), pero elimina la trampa para cuando las adquiera.

**Archivos modificados:**
- `.github/workflows/ci.yml`

## 2026:10:06 12:55

**Resumen:** Se reemplazó la plantilla de Pull Request (venía copiada del frontend) por una adaptada a esta librería compartida.

**Cambios:**
- Se reescribió `.github/pull_request_template.md`: la anterior era la del portal-cliente (comandos pnpm, invariantes de UI), que no aplica. La nueva es específica de `pf-pkg-platform-go`: librería sin dominio, puertos agnósticos (bus/worker/inbox), `auth` neutral respecto del proveedor, arch-test, compatibilidad hacia atrás y verificación con `go test -race` y `bus/bustest`.

**Archivos modificados:**
- `.github/pull_request_template.md`

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
