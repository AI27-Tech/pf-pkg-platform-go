# pf-pkg-platform-go

Plantilla de servicio compartida para los microservicios de AI27 (V1).

## Qué es y por qué existe

AI27 tiene siete módulos de backend (`bff`, `identidad`, `operacion`, `catalogos`, `reportes`, `mensajeria`, `realtime`). Sin un sustrato común, cada módulo re-inventaría arranque, configuración, pools de BD, logging, health checks, acceso a datos, workers y bus. `platform-go` es ese sustrato: un solo paquete Go que todos importan, para que los módulos se concentren en lógica de negocio.

**No conoce ningún dominio de negocio** — nada de `viaje`, `conversacion`, `custodio` ni similares. Si algo de este paquete empieza a necesitar saber qué es un "viaje", no pertenece aquí.

## Cómo funciona

Un módulo de negocio importa `platform-go` y usa sus componentes. El ciclo de vida típico es:

```
1. config.Load()          → Lee env vars, valida que existan (fail-fast)
2. pgxpool.New()          → Crea un pool de Postgres por rol de BD
3. auth.NewVerifier()     → Configura verificación JWT con el JWKS del IdP interno
4. server.New()           → Crea el servidor HTTP con /health y /ready
5. srv.Start()            → Escucha, drena conexiones, apaga ordenado al recibir SIGTERM
```

Un módulo con workers (outbox.Relay, consumidores del bus) los arranca con
`worker.RunGroup` en su raíz de composición; `platform-go` aporta las piezas
reutilizables, pero la raíz decide qué arranca (ADR 29 §6).

Cada paso es opcional — un módulo puede usar solo lo que necesite. Por ejemplo, `catalogos` no usa outbox ni Redis; `bff` usa auth pero no pgxpool directamente.

## Arquitectura interna

```
platform-go/
├── config/        ← Carga de variables de entorno con validación
│                     Todas las config vienen de env vars, nunca de archivos
│                     ni valores hardcodeados. Si falta una requerida, el
│                     servicio no arranca.
│
├── errors/        ← Taxonomía de errores estable
│                     Categorías: no_autorizado, no_encontrado, validacion,
│                     conflicto, degradado, roto, rate_limit.
│                     Cada categoría se traduce al mismo HTTP status por
│                     adaptadores HTTP e in-process (invariante I3).
│
├── health/        ← Handlers /health y /ready
│                     /health = liveness (proceso vivo, siempre 200)
│                     /ready = readiness (pools OK, dependencias OK)
│                     Cada servicio registra sus checks de readiness.
│
├── pgxpool/       ← Pool de Postgres por rol
│                     Un pool por rol de BD, nunca compartido entre roles
│                     (invariante I1 de ADR 17). Incluye health check para
│                     readiness.
│
├── outbox/        ← Publisher transaccional + Relay
│                     Publisher inserta en la tabla outbox del schema del
│                     llamador, dentro de la misma transacción que el cambio de
│                     dominio. Relay es el worker (ADR 29 §6) que drena esas
│                     filas y las publica por bus.Publisher, marcándolas
│                     publicadas solo tras el acuse del transporte.
│
├── bus/           ← Puerto agnóstico del bus de eventos (ADR 29)
│                     Interfaces Sobre, Publisher, Subscriber, Handler,
│                     Registry (lo inyecta el módulo) y Broadcaster. No fija
│                     transporte. Garantías: suscripción durable por módulo,
│                     orden por clave de agregado, dedup por EventoID, superficie
│                     amplia/restringida, venenoso→cuarentena, acuse tras commit,
│                     reintentos acotados.
│   ├── bustest/   ← Pruebas de contrato del puerto, reutilizables por adaptador
│   └── memory/    ← Adaptador en memoria (desarrollo y pruebas, NUNCA producción)
│   # El adaptador del broker V1 (on-premise) espera a que ADR 32 elija
│   # componente; no se construye aquí.
│
├── inbox/         ← Deduplicación idempotente de eventos consumidos
│                     InboxDDL + Registrar(tx, evento_id): el consumidor registra
│                     el EventoID en la misma tx que el efecto; un duplicado
│                     devuelve ErrYaProcesado y se omite el efecto (ADR 29 §1).
│
├── worker/        ← Unidad de trabajo embebida + ejecutor de grupo
│                     Interfaz Worker (Name/Run/Ready) y RunGroup, que arranca N
│                     workers, registra su readiness y espera el drenado al
│                     apagar. Sin pools, roles ni esquemas propios.
│
├── auth/          ← Verificador JWT con JWKS cacheado y refresco proactivo
│                     Verifica JWT localmente contra el JWKS del IdP interno
│                     (ADR 32 §2; el verificador no conoce al proveedor: URL y
│                     emisor se inyectan). Refresco proactivo antes del TTL con
│                     jitter (DEC-056) y gracia de 30 s de respaldo; fallo cerrado
│                     sin claves usables. Separa Audience (claim técnico `aud`) de
│                     Audiencia (dominio Cliente|Interno). Authenticator,
│                     Middleware común con forma de Error de la plataforma,
│                     ContextWithClaims y costura opcional de revocación.
│
├── observability/ ← OpenTelemetry (trazas + métricas)
│                     Init del tracer y meter provider con exportador OTLP HTTP y
│                     shutdown combinado. Helpers Inject/Extract de traza y
│                     WorkerMetrics (pendientes, antigüedad, procesados, fallidos,
│                     en cuarentena, duración) por worker/outbox/relay/consumidor.
│
├── redis/         ← Cliente Redis compartido
│                     Un solo cliente para estado vivo, pub/sub y sesión.
│                     Incluye health check para readiness.
│
├── server/        ← Servidor HTTP con graceful shutdown
│                     Orquesta: mux, health routes, signal handling,
│                     drenado de conexiones (30s timeout).
│                     Expone Mux() para que el módulo registre sus rutas.
│
├── tools/arch-test/   ← Verificador del grafo de arquitectura (ADR 17, E0-T6)
│                         Única copia del grafo (reglas.yaml embebido). Cada repo
│                         lo corre con `go run .../tools/arch-test@<version>`.
│
└── templates/Dockerfile  ← Plantilla de imagen parametrizada por CMD_PATH
                             La copian los repos con raíz de composición.
```

## Qué NO hace este paquete

- **No tiene lógica de negocio** — no sabe qué es un viaje o una incidencia.
- **No arranca workers por su cuenta** — aporta `worker`, `outbox.Relay` y los consumidores del bus como piezas reutilizables; cada raíz de composición decide arrancarlos con `worker.RunGroup`.
- **No fija el transporte del bus** — define el puerto `bus` y sus garantías (ADR 29). El broker V1 es on-premise y su producto lo elige ADR 32 (en *Propuesta*); el adaptador concreto vive aparte y no se construye hasta entonces. En desarrollo y pruebas se usa `bus/memory`.
- **No conoce el dominio del bus** — el módulo inyecta un `bus.Registry` (generado del registro de tipos del contrato) con los tipos válidos y la clave de orden.
- **No administra schemas de BD** — las migraciones son de cada módulo (incluye `outbox.OutboxDDL` e `inbox.InboxDDL` como referencia).
- **No genera clientes HTTP** — eso es de `pf-pkg-contracts`.

## Ejemplo: arrancar un módulo

```go
package main

import (
    "context"
    "log/slog"
    "os"

    "github.com/AI27-Tech/pf-pkg-platform-go/config"
    "github.com/AI27-Tech/pf-pkg-platform-go/health"
    "github.com/AI27-Tech/pf-pkg-platform-go/server"
)

func main() {
    cfg, err := config.Load()
    if err != nil {
        slog.Error("config failed", "error", err)
        os.Exit(1)
    }

    srv := server.New(cfg.HTTPAddr)

    // Registrar check de readiness (ejemplo: verificar BD).
    // srv.Registry().RegisterCheck("postgres", pool.CheckFunc())

    srv.MarkReady()
    if err := srv.Start(context.Background()); err != nil {
        slog.Error("server stopped", "error", err)
        os.Exit(1)
    }
}
```

```bash
DATABASE_URL="postgres://user:pass@localhost/mydb" \
REDIS_URL="redis://localhost:6379" \
SERVICE_NAME="operacion" \
go run .

# En otra terminal:
curl http://localhost:8080/health   # → {"status":"ok"}
curl http://localhost:8080/ready   # → {"status":"ok"} o {"status":"not_ready"}
```

## Topología V1

Ver `docs/adr/0017-topologia-de-despliegue-v1.md` y, para la forma multi-repo
compartida (un repo por módulo, tres deployables), `00-indice.md` del scaffold.

Tres deployables en V1. Cada raíz de composición vive en su propio repo
(`pf-svc-core`, `pf-svc-mensajeria`, `pf-svc-realtime`) y compila su `cmd/`
importando `platform-go`; aquí **no** viven esos `cmd/` (este repo es la
plantilla, no una raíz).

| Deployable | Repo de la raíz | Módulos incluidos |
|---|---|---|
| `core` | `pf-svc-core` | bff + identidad + operacion + catalogos + reportes |
| `mensajeria` | `pf-svc-mensajeria` | mensajeria |
| `realtime` | `pf-svc-realtime` | realtime |

Cada módulo conserva su propio `go.mod` y compila de forma independiente (invariante I5). En producción, los cinco módulos de `core` viven dentro de su raíz de composición.

### arch-test — verificación del grafo

El grafo de arquitectura (ADR 17) vive una sola vez, embebido en
`tools/arch-test/reglas.yaml`. Cada repo de servicio lo corre en su CI:

```bash
go run github.com/AI27-Tech/pf-pkg-platform-go/tools/arch-test@<version>
```

Verifica que ningún `pf-svc-*` requiera otro `pf-svc-*` (salvo `pf-svc-core`),
que cada módulo solo importe los clientes generados de sus aristas declaradas,
que el grafo sea acíclico y la regla de la raíz de composición. Un módulo fuera
del grafo falla.

### Imagen Docker

`templates/Dockerfile` es la plantilla que copian los repos con raíz de
composición. `CMD_PATH` es obligatorio (sin default) y apunta al `main` a
compilar; el token de los repos privados (`AI27-Tech/*`) se inyecta como secreto
de BuildKit, nunca como `ARG` ni capa persistente:

```bash
DOCKER_BUILDKIT=1 docker build \
  --secret id=gh_token,env=GH_TOKEN \
  --build-arg CMD_PATH=./cmd/core -t ai27/core .
```

## Convención

- Identificadores técnicos en inglés.
- Sustantivos de dominio (`viaje`, `operacion`, `mensajeria`) en español.
- Ver `naming-conventions.md` para la regla completa.

## Testing

```bash
go build ./...    # compila
go vet ./...      # sin warnings
go test ./...     # tests unitarios
```

Para tests de integración se necesita Postgres corriendo. Los tests que lo
requieren se **saltan** (`t.Skip`) cuando `PLATFORM_GO_TEST_DB` no está
definida, así que `go test ./...` pasa sin base de datos (p. ej. CI sin BD).
Para ejecutarlos, apunta la variable a un DSN con privilegios de `CREATE ROLE`
y `CREATE SCHEMA` (crean roles/schemas efímeros que se limpian al terminar):

```bash
PLATFORM_GO_TEST_DB="postgres:///postgres" go test ./...
```

Cubren: aislamiento por schema (`permission denied for schema`), supervivencia
de la outbox a un reinicio, y propagación de traza HTTP → outbox.

## Publicar y consumir eventos (outbox + bus)

El `Publisher` inserta la **capa de dominio del sobre** —los mismos campos que
`Sobre` en `pf-pkg-contracts`— en la tabla `outbox` del schema del llamador,
dentro de su transacción. El `Relay` (un `worker.Worker`) la drena y, tras el
acuse del transporte (`bus.Publisher`), marca la fila como publicada (ADR 29).

```go
pub := outbox.NewPublisher()

tx, _ := pool.Begin(ctx)
err := pub.Publish(ctx, tx, outbox.Event{
    Tipo:       "viaje.creado", // en el registro de tipos del contrato
    AgregadoID: viajeID,        // clave de orden; obligatoria (sin clave global)
    Payload:    evento,         // se serializa a JSONB
    // EventoID (UUID), Version (1), Sensibilidad (amplia) y OcurridoEn (now)
    // se completan solos si se dejan vacíos.
})
if err != nil { tx.Rollback(ctx); return }
tx.Commit(ctx) // el evento y el cambio de dominio se comprometen juntos (P17)
```

El contexto de traza activo se persiste en la columna `trace_context` de la
fila, así que el trazo de la petición HTTP continúa hasta el evento. La tabla se
crea con `outbox.OutboxDDL` desde las migraciones de cada módulo (este paquete
no administra schemas).

La raíz arranca el relay y los consumidores durables como workers. El bus
concreto en desarrollo es `bus/memory`; en producción, el adaptador del broker
(en espera de ADR 32). El `bus.Registry` lo inyecta el módulo desde el registro
de tipos generado del contrato:

```go
b := memory.Nuevo(registryDelModulo, memory.Opciones{})      // dev; prod: adaptador del broker
relay := outbox.NewRelay("outbox-relay:operacion", pool, b, outbox.RelayOpciones{})

consumidor, _ := b.Subscribe("operacion", bus.SensibilidadAmplia, nil, handler)

// La raíz arranca ambos y espera su drenado al apagar.
_ = worker.RunGroup(ctx, srv.Registry(), relay, consumidor)
```

El consumidor usa `inbox.Registrar` dentro de la transacción que aplica el
efecto para que un reentrega no produzca un segundo efecto (idempotencia por
`EventoID`).
