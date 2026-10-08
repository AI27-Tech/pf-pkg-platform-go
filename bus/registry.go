package bus

import "fmt"

// TipoRegistrado es la forma estructural MÍNIMA que el molde del Registry
// necesita: el nombre del tipo de evento y la versión de contrato de su
// payload. El deployable la llena desde el registro generado del contrato
// (api-client-go/registro), traduciendo cada entrada a este par.
//
// A propósito NO lleva Agregado, Sensibilidad ni Productor: el Registry solo
// consume Tipo y Version (ver Allowed y OrderingKey). Agregar aquí campos que
// el molde no usa acoplaría platform-go a detalles del dominio sin ganar nada
// (buenas prácticas Go §5: no sobre-abstraer). Si algún día Allowed u
// OrderingKey necesitan más datos, se amplía entonces, no por anticipación.
type TipoRegistrado struct {
	// Tipo es el nombre del tipo de evento, p. ej. "viaje.estado_cambiado".
	Tipo string
	// Version es la versión del contrato del payload de ese tipo.
	Version int
}

// registry es la implementación concreta de Registry que NuevoRegistry
// construye. Indexa los tipos permitidos por la pareja (tipo, versión), de modo
// que una versión equivocada de un tipo conocido también se considere no
// registrada (venenosa).
type registry struct {
	permitidos map[string]bool
}

// NuevoRegistry construye un Registry a partir de la lista de tipos registrados
// que el deployable aporta como DATOS. platform-go no conoce nombres de evento
// (ver doc de package bus): el molde vive aquí, los datos los pone el módulo.
// Así, cuando un módulo se extraiga a su propio proceso (ADR 17, invariante I5),
// basta volver a alimentar este constructor con su lista de tipos; no hay que
// mover ni duplicar lógica de adaptador.
func NuevoRegistry(tipos []TipoRegistrado) Registry {
	permitidos := make(map[string]bool, len(tipos))
	for _, t := range tipos {
		permitidos[claveTipo(t.Tipo, t.Version)] = true
	}
	return &registry{permitidos: permitidos}
}

// claveTipo compone la clave de índice (tipo, versión). Misma forma que usan
// los dobles de prueba, para que el Registry real sea intercambiable con ellos.
func claveTipo(tipo string, version int) string {
	return fmt.Sprintf("%s@%d", tipo, version)
}

// Allowed indica si la pareja (tipo, versión) está en la lista registrada. Un
// tipo ausente, o un tipo conocido con una versión que no está registrada, es
// venenoso y el adaptador lo manda a cuarentena.
func (r *registry) Allowed(tipo string, version int) bool {
	return r.permitidos[claveTipo(tipo, version)]
}

// OrderingKey devuelve SIEMPRE el AgregadoID del sobre. La clave de orden ya
// viene compuesta por el PRODUCTOR al publicar: es él quien decide qué va en
// AgregadoID (para la mayoría de los eventos, el id del agregado; para
// catalogo.actualizado, la composición "entidad + entidad_id" que arma
// catalogos en su outbox, otra fase). El Registry no recompone la clave: confía
// en que AgregadoID ya trae el valor correcto. Por eso el molde no necesita
// conocer el Agregado de cada tipo, y por eso esta función no mira la lista de
// tipos registrados.
func (r *registry) OrderingKey(s Sobre) string {
	return s.AgregadoID
}
