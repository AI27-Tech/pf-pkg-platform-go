// Package observability ofrece inicialización de OpenTelemetry para trazas
// y métricas. Cada servicio importa este paquete una vez al arrancar;
// los componentes individuales usan la API de OTel directamente.
package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Config contiene la configuración para la inicialización de OpenTelemetry.
type Config struct {
	// ServiceName es el nombre lógico del servicio (ej. "operacion").
	ServiceName string
	// ExporterURL es el endpoint del exportador OTLP HTTP (ej. "http://localhost:4318").
	ExporterURL string
	// SampleRate es la probabilidad de muestreo de una traza (0.0 a 1.0).
	// Por defecto es 1.0 (muestrear todo) si no se establece.
	SampleRate float64
}

// InitTracerProvider configura el tracer provider global de OpenTelemetry.
// Devuelve una función de shutdown que debe llamarse al salir del proceso.
func InitTracerProvider(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("observability: service_name no debe estar vacío")
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 1.0
	}

	// Crear el exportador OTLP HTTP.
	var opts []otlptracehttp.Option
	if cfg.ExporterURL != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(cfg.ExporterURL))
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("observability: crear exportador OTLP: %w", err)
	}

	// Crear recurso con identidad del servicio.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: crear recurso: %w", err)
	}

	// Crear tracer provider con procesador por lotes.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRate)),
	)

	otel.SetTracerProvider(tp)

	// Establecer el propagador de contexto W3C (traceparent/tracestate), de modo
	// que el contexto de traza cruce procesos: entra por HTTP, se propaga a la
	// outbox y de ahí al bus. Sin esto, cada salto iniciaría una traza nueva.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer devuelve un tracer del provider global para el nombre de instrumentación dado.
func Tracer(instrumentationName string) trace.Tracer {
	return otel.Tracer(instrumentationName)
}
