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
		{CategoryUnknown, "desconocido"},
	}

	for _, tt := range tests {
		if got := tt.cat.String(); got != tt.want {
			t.Errorf("Category(%d).String() = %q, se esperaba %q", tt.cat, got, tt.want)
		}
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
