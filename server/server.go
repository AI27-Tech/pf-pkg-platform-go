// Package server ofrece el arranque uniforme del servidor HTTP con
// apagado ordenado, drenado de conexiones y configuración de los
// componentes comunes (health, OTel, graceful shutdown).
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/health"
)

// Server envuelve un http.Server con apagado ordenado y health checks.
type Server struct {
	httpServer *http.Server
	mux        *http.ServeMux
	registry   *health.Registry
	logger     *slog.Logger
	addr       string
}

// Option configura el servidor.
type Option func(*Server)

// WithLogger establece un logger personalizado.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		s.logger = logger
	}
}

// WithAddr establece la dirección de escucha (sobreescribe la config).
func WithAddr(addr string) Option {
	return func(s *Server) {
		s.addr = addr
	}
}

// New crea un nuevo servidor con health checks registrados.
// El servidor NO empieza a escuchar hasta que se llama a Start().
func New(addr string, opts ...Option) *Server {
	s := &Server{
		addr:     addr,
		mux:      http.NewServeMux(),
		registry: health.NewRegistry(),
		logger:   slog.Default(),
	}

	for _, opt := range opts {
		opt(s)
	}

	// Registrar rutas de health.
	health.RegisterRoutes(s.mux, s.registry)

	s.httpServer = &http.Server{
		Addr:         s.addr,
		Handler:      s.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

// Mux devuelve el http.ServeMux subyacente para registrar rutas de la aplicación.
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// Registry devuelve el registro de health para registrar verificaciones de readiness.
func (s *Server) Registry() *health.Registry {
	return s.registry
}

// Logger devuelve el logger del servidor.
func (s *Server) Logger() *slog.Logger {
	return s.logger
}

// Start empieza a escuchar y bloquea hasta recibir una señal de apagado.
// Realiza un apagado ordenado con un timeout configurable.
func (s *Server) Start(ctx context.Context) error {
	// Canal para errores del servidor.
	errCh := make(chan error, 1)

	// Iniciar el servidor en una goroutine.
	go func() {
		s.logger.Info("servidor iniciando", slog.String("addr", s.addr))
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Esperar señal de interrupción.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("servidor: listen: %w", err)
	case sig := <-quit:
		s.logger.Info("servidor apagando", slog.String("signal", sig.String()))
	}

	// Marcar como no listo antes de drenar.
	s.registry.SetReady(false)

	// Dar hasta 30 segundos a las peticiones en vuelo para completarse.
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("servidor: shutdown: %w", err)
	}

	s.logger.Info("servidor detenido ordenadamente")
	return nil
}

// MarkReady marca el servidor como listo para recibir tráfico.
func (s *Server) MarkReady() {
	s.registry.SetReady(true)
}
