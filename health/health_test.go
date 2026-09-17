package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLivenessHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	LivenessHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("liveness status = %d, se esperaba %d", w.Code, http.StatusOK)
	}

	var resp healthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decodificar respuesta: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("liveness status = %q, se esperaba %q", resp.Status, "ok")
	}
}

func TestReadinessHandler_NoListo(t *testing.T) {
	reg := NewRegistry()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessHandler(reg).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness status = %d, se esperaba %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestReadinessHandler_Listo(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterCheck("test", func(ctx context.Context) error {
		return nil
	})
	reg.SetReady(true)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessHandler(reg).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("readiness status = %d, se esperaba %d", w.Code, http.StatusOK)
	}

	var resp healthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decodificar respuesta: %v", err)
	}
	if resp.Checks["test"] != "ok" {
		t.Errorf("check test = %q, se esperaba %q", resp.Checks["test"], "ok")
	}
}

func TestReadinessHandler_CheckFallido(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterCheck("fallido", func(ctx context.Context) error {
		return context.DeadlineExceeded
	})
	reg.SetReady(true)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessHandler(reg).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness status = %d, se esperaba %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestRegisterCheckDuplicadoPanica(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("se esperaba panic por check duplicado")
		}
	}()

	reg := NewRegistry()
	fn := func(ctx context.Context) error { return nil }
	reg.RegisterCheck("dup", fn)
	reg.RegisterCheck("dup", fn) // debe panic
}
