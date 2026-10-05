package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitMeterProvider configura el meter provider global de OpenTelemetry con un
// exportador OTLP HTTP, reutilizando la misma Config que el tracer. Devuelve la
// función de shutdown que drena las métricas pendientes al salir.
//
// Los nombres de instrumento son propuesta del paquete, no contrato (01 P3):
// pueden ajustarse sin romper a los consumidores.
func InitMeterProvider(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("observability: service_name no debe estar vacío")
	}

	var opts []otlpmetrichttp.Option
	if cfg.ExporterURL != "" {
		opts = append(opts, otlpmetrichttp.WithEndpoint(cfg.ExporterURL))
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("observability: crear exportador OTLP de métricas: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceNameKey.String(cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: crear recurso de métricas: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(10*time.Second),
		)),
	)
	otel.SetMeterProvider(mp)
	return mp.Shutdown, nil
}

// Init inicializa trazas y métricas de una vez y devuelve un shutdown combinado
// que cierra ambos providers. Es la forma recomendada para una raíz de
// composición: una sola llamada, un solo defer.
func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	shutdownTrace, err := InitTracerProvider(ctx, cfg)
	if err != nil {
		return nil, err
	}
	shutdownMeter, err := InitMeterProvider(ctx, cfg)
	if err != nil {
		// Dejar las trazas en estado consistente si las métricas fallan.
		_ = shutdownTrace(ctx)
		return nil, err
	}
	return func(ctx context.Context) error {
		errT := shutdownTrace(ctx)
		errM := shutdownMeter(ctx)
		if errT != nil {
			return errT
		}
		return errM
	}, nil
}

// WorkerMetrics agrupa los instrumentos mínimos que ADR 29 §6 pide por worker,
// outbox, relay y consumidor del bus. Los módulos los comparten para que
// "la outbox envejece de forma observable" sea verificable.
//
// Sin payload ni identificadores sensibles en los atributos (01 P3): etiquetar
// solo con el nombre del worker, el tipo de evento y el resultado.
type WorkerMetrics struct {
	// Procesados cuenta sobres procesados con éxito (acusados).
	Procesados metric.Int64Counter
	// Fallidos cuenta entregas fallidas (antes de agotar reintentos).
	Fallidos metric.Int64Counter
	// EnCuarentena cuenta sobres enviados a DLQ/cuarentena.
	EnCuarentena metric.Int64Counter
	// Pendientes observa, por callback, el tamaño de la cola/backlog.
	Pendientes metric.Int64ObservableGauge
	// AntiguedadSegundos observa la antigüedad del pendiente más viejo (outbox).
	AntiguedadSegundos metric.Float64ObservableGauge
	// Duracion mide la latencia de procesamiento de un sobre.
	Duracion metric.Float64Histogram
}

// NewWorkerMetrics crea los instrumentos bajo el meter dado (p. ej.
// "platform-go/worker"). Devuelve error si algún instrumento no se puede crear.
func NewWorkerMetrics(meterName string) (*WorkerMetrics, error) {
	m := otel.Meter(meterName)
	var err error
	wm := &WorkerMetrics{}

	if wm.Procesados, err = m.Int64Counter("ai27_worker_procesados_total",
		metric.WithDescription("Sobres procesados con éxito y acusados")); err != nil {
		return nil, err
	}
	if wm.Fallidos, err = m.Int64Counter("ai27_worker_fallidos_total",
		metric.WithDescription("Entregas fallidas antes de agotar reintentos")); err != nil {
		return nil, err
	}
	if wm.EnCuarentena, err = m.Int64Counter("ai27_worker_cuarentena_total",
		metric.WithDescription("Sobres enviados a DLQ/cuarentena")); err != nil {
		return nil, err
	}
	if wm.Pendientes, err = m.Int64ObservableGauge("ai27_worker_pendientes",
		metric.WithDescription("Trabajo pendiente observable (backlog)")); err != nil {
		return nil, err
	}
	if wm.AntiguedadSegundos, err = m.Float64ObservableGauge("ai27_outbox_antiguedad_segundos",
		metric.WithDescription("Antigüedad del pendiente más viejo en la outbox")); err != nil {
		return nil, err
	}
	if wm.Duracion, err = m.Float64Histogram("ai27_worker_duracion_segundos",
		metric.WithDescription("Latencia de procesamiento de un sobre")); err != nil {
		return nil, err
	}
	return wm, nil
}
