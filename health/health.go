// Package health ofrece handlers HTTP de liveness y readiness que cada
// servicio registra en su mux. La liveness solo verifica que el proceso
// esté vivo; la readiness verifica que las dependencias críticas estén
// alcanzables (pools de BD, Redis, etc.).
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// CheckFunc es una función que devuelve nil si la verificación pasa.
type CheckFunc func(ctx context.Context) error

// Registry contiene las verificaciones de readiness que los servicios registran al arrancar.
type Registry struct {
	mu      sync.RWMutex
	checks  map[string]CheckFunc
	ready   bool
	readyMu sync.RWMutex
}

// NewRegistry crea un registro de health checks.
func NewRegistry() *Registry {
	return &Registry{
		checks: make(map[string]CheckFunc),
	}
}

// RegisterCheck agrega una verificación de readiness con nombre.
// Panic si ya existe una verificación con el mismo nombre.
func (r *Registry) RegisterCheck(name string, fn CheckFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.checks[name]; exists {
		panic("health: verificación duplicada: " + name)
	}
	r.checks[name] = fn
}

// SetReady marca el servicio como listo. Se llama después de que todas las
// verificaciones pasan al arrancar.
func (r *Registry) SetReady(ready bool) {
	r.readyMu.Lock()
	defer r.readyMu.Unlock()
	r.ready = ready
}

// IsReady devuelve si el servicio está listo actualmente.
func (r *Registry) IsReady() bool {
	r.readyMu.RLock()
	defer r.readyMu.RUnlock()
	return r.ready
}

// healthResponse es la estructura JSON que devuelven ambos endpoints.
type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// LivenessHandler devuelve un http.Handler para el endpoint /health.
// Siempre devuelve 200 OK si el proceso está ejecutándose.
func LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
	})
}

// ReadinessHandler devuelve un http.Handler para el endpoint /ready.
// Ejecuta todas las verificaciones registradas y devuelve 200 si el servicio
// está marcado como listo y todas pasan, o 503 en caso contrario.
func ReadinessHandler(reg *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !reg.IsReady() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(healthResponse{Status: "not_ready"})
			return
		}

		reg.mu.RLock()
		checks := make(map[string]CheckFunc, len(reg.checks))
		for k, v := range reg.checks {
			checks[k] = v
		}
		reg.mu.RUnlock()

		results := make(map[string]string, len(checks))
		allOK := true

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		for name, fn := range checks {
			if err := fn(ctx); err != nil {
				results[name] = "falló: " + err.Error()
				allOK = false
			} else {
				results[name] = "ok"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if allOK {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(healthResponse{Status: "ok", Checks: results})
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(healthResponse{Status: "degraded", Checks: results})
		}
	})
}

// RegisterRoutes registra /health y /ready en el mux dado.
func RegisterRoutes(mux *http.ServeMux, reg *Registry) {
	mux.Handle("/health", LivenessHandler())
	mux.Handle("/ready", ReadinessHandler(reg))
}
