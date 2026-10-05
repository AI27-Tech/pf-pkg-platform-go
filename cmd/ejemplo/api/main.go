// Command ejemplo es la plantilla mínima de una raíz de composición.
//
// No es un módulo de negocio: es el punto de partida que un servicio nuevo
// COPIA para arrancar con config, observabilidad, health checks y apagado
// ordenado sin reimplementar el sustrato (criterio de aceptación E0-T1:
// "un módulo nuevo se levanta copiando un directorio mínimo").
//
// El binario se compila con la plantilla templates/Dockerfile pasando
// CMD_PATH (p. ej. CMD_PATH=./cmd/core), así que un servicio real crea, por
// ejemplo, `cmd/core/api/main.go` a imagen de este archivo y registra ahí sus
// propios módulos (rutas, pools por rol, workers de outbox y del bus).
//
// Arranque típico:
//
//	SERVICE_NAME=ejemplo HTTP_ADDR=:8080 go run ./cmd/ejemplo/api
//	curl localhost:8080/health   # liveness  -> {"status":"ok"}
//	curl localhost:8080/ready    # readiness -> {"status":"ok"} | 503
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/AI27-Tech/pf-pkg-platform-go/config"
	"github.com/AI27-Tech/pf-pkg-platform-go/observability"
	"github.com/AI27-Tech/pf-pkg-platform-go/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("el servicio terminó con error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// 1. Config desde variables de entorno; falla temprano si falta alguna.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("arrancando", slog.String("service", cfg.ServiceName))

	// 2. Observabilidad: tracer provider + propagador W3C globales.
	//    El shutdown drena los spans pendientes al salir.
	shutdownOtel, err := observability.InitTracerProvider(ctx, observability.Config{
		ServiceName: cfg.ServiceName,
		ExporterURL: cfg.OtelExporterURL,
	})
	if err != nil {
		// La observabilidad no debe impedir arrancar en local sin colector;
		// se registra y se continúa. Un servicio real puede endurecer esto.
		logger.Warn("observabilidad no inicializada", slog.Any("error", err))
	} else {
		defer func() { _ = shutdownOtel(context.Background()) }()
	}

	// 3. Servidor HTTP con /health y /ready ya montados y span por petición.
	srv := server.New(cfg.HTTPAddr, server.WithLogger(logger))

	// Aquí un módulo real:
	//   - crea sus pools por rol:  pool, _ := pgxpool.New(ctx, "op_viajes", cfg.DatabaseURL)
	//   - registra readiness:      srv.Registry().RegisterCheck("postgres", pool.CheckFunc())
	//   - registra sus rutas:      srv.Mux().Handle("/api/v1/...", handler)
	// Este ejemplo solo expone una ruta trivial para mostrar el patrón.
	srv.Mux().HandleFunc("/api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pong":true}`))
	})

	// 4. Marcar listo y bloquear hasta SIGTERM/SIGINT; Start drena en 30s.
	srv.MarkReady()
	return srv.Start(ctx)
}
