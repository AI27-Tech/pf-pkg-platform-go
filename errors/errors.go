// Package errors define una taxonomía estable de errores para la plataforma.
// Cada categoría se traduce de la misma forma por adaptadores HTTP e in-process
// (invariante I3 de ADR 17).
package errors

import (
	"errors"
	"fmt"
	"net/http"
)

// Categorías de error — estables, compartidas entre todos los servicios.
type Category int

const (
	// CategoryUnknown es el valor cero y no debe usarse explícitamente.
	CategoryUnknown Category = iota
	// CategoryNoAutorizado — el llamador no está autenticado o autorizado.
	CategoryNoAutorizado
	// CategoryNoEncontrado — el recurso solicitado no existe.
	CategoryNoEncontrado
	// CategoryValidacion — el payload de la petición es inválido.
	CategoryValidacion
	// CategoryConflicto — la petición entra en conflicto con el estado actual (ej. 409).
	CategoryConflicto
	// CategoryDegradado — una dependencia está degradada pero el servicio funciona.
	CategoryDegradado
	// CategoryRoto — el servicio o una dependencia crítica está rota.
	CategoryRoto
	// CategoryRateLimit — demasiadas peticiones.
	CategoryRateLimit
	// CategoryProhibido — autenticado pero sin la facultad para esta acción (403).
	// Distinto de NoAutorizado (401, sin autenticar o sin sesión válida).
	CategoryProhibido
)

// PlatformError es el tipo de error canónico de la plataforma.
type PlatformError struct {
	Category Category
	Message  string
	Err      error // error interno, si existe
	// Codigo es un código de máquina específico, más fino que Category.String().
	// Opcional: cuando está vacío, el borde HTTP usa Category.String() como hoy.
	// Lo rellenan los conflictos que el cliente necesita distinguir (p. ej.
	// `cierre_anticipado_solo_por_incidencia`), sin crear una categoría por cada
	// variante de 409.
	Codigo string
}

func (e *PlatformError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *PlatformError) Unwrap() error {
	return e.Err
}

// HTTPStatus mapea una categoría a su código de estado HTTP.
func (c Category) HTTPStatus() int {
	switch c {
	case CategoryNoAutorizado:
		return http.StatusUnauthorized
	case CategoryNoEncontrado:
		return http.StatusNotFound
	case CategoryValidacion:
		return http.StatusBadRequest
	case CategoryConflicto:
		return http.StatusConflict
	case CategoryDegradado:
		return http.StatusServiceUnavailable
	case CategoryRoto:
		return http.StatusBadGateway
	case CategoryRateLimit:
		return http.StatusTooManyRequests
	case CategoryProhibido:
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

// String devuelve el nombre legible de la categoría.
func (c Category) String() string {
	switch c {
	case CategoryNoAutorizado:
		return "no_autorizado"
	case CategoryNoEncontrado:
		return "no_encontrado"
	case CategoryValidacion:
		return "validacion"
	case CategoryConflicto:
		return "conflicto"
	case CategoryDegradado:
		return "degradado"
	case CategoryRoto:
		return "roto"
	case CategoryRateLimit:
		return "rate_limit"
	case CategoryProhibido:
		return "prohibido"
	default:
		return "desconocido"
	}
}

// Funciones constructoras.

func NewNoAutorizado(msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryNoAutorizado, Message: fmt.Sprintf(msg, args...)}
}

func NewNoEncontrado(msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryNoEncontrado, Message: fmt.Sprintf(msg, args...)}
}

func NewValidacion(msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryValidacion, Message: fmt.Sprintf(msg, args...)}
}

func NewConflicto(msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryConflicto, Message: fmt.Sprintf(msg, args...)}
}

// NewConflictoCodigo es un conflicto (409) con un código de máquina específico
// que el cliente puede distinguir (p. ej. `cierre_anticipado_solo_por_incidencia`).
// El borde HTTP emite ese `codigo` en vez del genérico "conflicto".
func NewConflictoCodigo(codigo, msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryConflicto, Message: fmt.Sprintf(msg, args...), Codigo: codigo}
}

// NewProhibido es un 403: el llamador está autenticado pero no tiene la facultad
// para la acción. Distinto de NewNoAutorizado (401, sin autenticar).
func NewProhibido(msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryProhibido, Message: fmt.Sprintf(msg, args...)}
}

func NewDegradado(msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryDegradado, Message: fmt.Sprintf(msg, args...)}
}

func NewDegradadoErr(err error, msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryDegradado, Message: fmt.Sprintf(msg, args...), Err: err}
}

func NewRoto(msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryRoto, Message: fmt.Sprintf(msg, args...)}
}

func NewRotoErr(err error, msg string, args ...any) *PlatformError {
	return &PlatformError{Category: CategoryRoto, Message: fmt.Sprintf(msg, args...), Err: err}
}

// AsCategory extrae la Categoría de un PlatformError envuelto.
// Devuelve CategoryUnknown si el error no es un PlatformError.
func AsCategory(err error) Category {
	var pe *PlatformError
	if errors.As(err, &pe) {
		return pe.Category
	}
	return CategoryUnknown
}

// CodigoDe devuelve el código de máquina que el borde HTTP debe emitir: el
// Codigo específico del PlatformError si lo trae, y si no, el genérico de la
// categoría (Category.String()). Un error que no es PlatformError cae en el
// String() de CategoryUnknown ("desconocido"). Centraliza la regla para que
// cada responder no la repita.
func CodigoDe(err error) string {
	var pe *PlatformError
	if errors.As(err, &pe) {
		if pe.Codigo != "" {
			return pe.Codigo
		}
		return pe.Category.String()
	}
	return CategoryUnknown.String()
}
