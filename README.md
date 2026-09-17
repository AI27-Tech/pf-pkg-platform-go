# pf-pkg-platform-go

Plantilla de servicio compartida para los microservicios de AI27 (V1).

## Qué es y por qué existe

AI27 tiene siete módulos de backend (`bff`, `identidad`, `operacion`, `catalogos`, `reportes`, `mensajeria`, `realtime`). Sin un sustrato común, cada módulo re-inventaría arranque, configuración, pools de BD, logging, health checks y acceso a datos. `platform-go` es ese sustrato: un solo paquete Go que todos importan, para que los módulos se concentren en lógica de negocio.

**No conoce ningún dominio de negocio** — nada de `viaje`, `conversacion`, `custodio` ni similares. Si algo de este paquete empieza a necesitar saber qué es un "viaje", no pertenece aquí.

## Cómo funciona

Un módulo de negocio importa `platform-go` y usa sus componentes. El ciclo de vida típico es:

```
1. config.Load()          → Lee env vars, valida que existan (fail-fast)
2. pgxpool.New()          → Crea un pool de Postgres por rol de BD
3. auth.NewVerifier()     → Configura verificación JWT con JWKS de Cognito
4. server.New()           → Crea el servidor HTTP con /health y /ready
5. srv.Start()            → Escucha, drena conexiones, apaga ordenado al recibir SIGTERM
```

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
│                     /readiness = readiness (pools OK, dependencias OK)
│                     Cada servicio registra sus checks de readiness.
│
├── pgxpool/       ← Pool de Postgres por rol
│                     Un pool por rol de BD, nunca compartido entre roles
│                     (invariante I1 de ADR 17). Incluye health check para
│                     readiness.
│
├── outbox/        ← Publisher transaccional de eventos
│                     Inserta en la tabla outbox del schema del llamador,
│                     dentro de la misma transacción que el cambio de dominio.
│                     El worker que drena la outbox es responsabilidad de cada
│                     cmd/ de composición (E0-T10), no de este paquete.
│
├── auth/          ← Verificador JWT con JWKS cacheado
│                     Verifica JWT localmente contra el JWKS de Cognito.
│                     Caché de claves con TTL. Fallo cerrado: si el JWKS es
│                     inalcanzable y la caché expiró, rechaza (no acepta sin
│                     verificar). Extrae claims: rol, tenant, Audiencia,
│                     sesion_id. Incluye middleware HTTP.
│
├── observability/ ← OpenTelemetry (trazas + métricas)
│                     Init del tracer provider con exportador OTLP HTTP.
│                     Propagación de trazas por defecto en HTTP, outbox y bus.
│
├── redis/         ← Cliente Redis compartido
│                     Un solo cliente para estado vivo, pub/sub y sesión.
│                     Incluye health check para readiness.
│
└── server/        ← Servidor HTTP con graceful shutdown
                      Orquesta: mux, health routes, signal handling,
                      drenado de conexiones (30s timeout).
                      Expone Mux() para que el módulo registre sus rutas.
```

## Qué NO hace este paquete

- **No tiene lógica de negocio** — no sabe qué es un viaje o una incidencia.
- **No implementa workers** — el worker de outbox y el de bus son de E0-T10, en cada `cmd/` de composición.
- **No fija un broker** — el outbox publisher es agnóstico a SNS/SQS/Kafka (ADR 2 abierto).
- **No administra schemas de BD** — las migraciones son de cada módulo.
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

Ver `docs/adr/0017-topologia-de-despliegue-v1.md`.

Tres deployables en V1, cada uno compila un `cmd/` que importa `platform-go`:

| Deployable | Módulos incluidos |
|---|---|
| `cmd/core` | bff + identidad + operacion + catalogos + reportes |
| `cmd/mensajeria` | mensajeria |
| `cmd/realtime` | realtime |

Cada módulo conserva su propio `go.mod` y compila de forma independiente (invariante I5). En producción, viven dentro de la raíz de composición.

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

Para tests de integración se necesita Postgres y Redis corriendo (ver sección de tests de integración en el spec E0-T1).
