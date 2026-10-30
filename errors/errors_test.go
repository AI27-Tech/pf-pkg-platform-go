package errors

import (
	"errors"
	"testing"
)

func TestCategoryHTTPStatus(t *testing.T) {
	tests := []struct {
		cat    Category
		status int
	}{
		{CategoryNoAutorizado, 401},
		{CategoryNoEncontrado, 404},
		{CategoryValidacion, 400},
		{CategoryConflicto, 409},
		{CategoryDegradado, 503},
		{CategoryRoto, 502},
		{CategoryRateLimit, 429},
		{CategoryProhibido, 403},
		{CategoryUnknown, 500},
	}

	for _, tt := range tests {
		if got := tt.cat.HTTPStatus(); got != tt.status {
			t.Errorf("Category(%d).HTTPStatus() = %d, se esperaba %d", tt.cat, got, tt.status)
		}
	}
}

func TestCategoryString(t *testing.T) {
	tests := []struct {
		cat  Category
		want string
	}{
		{CategoryNoAutorizado, "no_autorizado"},
		{CategoryNoEncontrado, "no_encontrado"},
		{CategoryDegradado, "degradado"},
		{CategoryRoto, "roto"},
		{CategoryProhibido, "prohibido"},
		{CategoryUnknown, "desconocido"},
	}

	for _, tt := range tests {
		if got := tt.cat.String(); got != tt.want {
			t.Errorf("Category(%d).String() = %q, se esperaba %q", tt.cat, got, tt.want)
		}
	}
}

// TestNewConflictoCodigo verifica que el código específico se conserva y que la
// categoría sigue siendo Conflicto (409).
func TestNewConflictoCodigo(t *testing.T) {
	err := NewConflictoCodigo("cierre_anticipado_requiere_incidencia", "falta incidencia")
	if err.Codigo != "cierre_anticipado_requiere_incidencia" {
		t.Errorf("Codigo = %q", err.Codigo)
	}
	if err.Category != CategoryConflicto {
		t.Errorf("Category = %d, se esperaba Conflicto", err.Category)
	}
	// Un conflicto sin código deja Codigo vacío (el borde usa String()).
	if NewConflicto("x").Codigo != "" {
		t.Error("NewConflicto no debe rellenar Codigo")
	}
}

// TestCodigoDe cubre las tres ramas: código específico, genérico de categoría,
// y error ajeno a la taxonomía.
func TestCodigoDe(t *testing.T) {
	if got := CodigoDe(NewConflictoCodigo("cierre_anticipado_requiere_incidencia", "x")); got != "cierre_anticipado_requiere_incidencia" {
		t.Errorf("código específico: %q", got)
	}
	if got := CodigoDe(NewConflicto("x")); got != "conflicto" {
		t.Errorf("genérico de categoría: %q", got)
	}
	if got := CodigoDe(errors.New("plano")); got != "desconocido" {
		t.Errorf("error plano: %q", got)
	}
}

// TestNewProhibido fija el 403 como categoría propia, distinta de NoAutorizado.
func TestNewProhibido(t *testing.T) {
	err := NewProhibido("sin facultad")
	if err.Category != CategoryProhibido {
		t.Errorf("Category = %d, se esperaba Prohibido", err.Category)
	}
	if AsCategory(err) != CategoryProhibido {
		t.Error("AsCategory no recupera Prohibido")
	}
}

func TestPlatformError_Error(t *testing.T) {
	err := NewNoEncontrado("viaje %d no encontrado", 42)
	if got := err.Error(); got != "viaje 42 no encontrado" {
		t.Errorf("Error() = %q", got)
	}
}

func TestPlatformError_Unwrap(t *testing.T) {
	inner := errors.New("conexión rechazada")
	err := NewDegradadoErr(inner, "bd inalcanzable")
	if !errors.Is(err, inner) {
		t.Error("se esperaba que errors.Is encontrara el error interno")
	}
}

func TestAsCategory(t *testing.T) {
	err := NewNoAutorizado("no autorizado")
	if cat := AsCategory(err); cat != CategoryNoAutorizado {
		t.Errorf("AsCategory = %d, se esperaba %d", cat, CategoryNoAutorizado)
	}

	plainErr := errors.New("error plano")
	if cat := AsCategory(plainErr); cat != CategoryUnknown {
		t.Errorf("AsCategory para error plano = %d, se esperaba %d", cat, CategoryUnknown)
	}
}
