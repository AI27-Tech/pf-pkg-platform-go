package main

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed reglas.yaml
var reglasYAML []byte

// Reglas es el grafo de arquitectura declarado (reglas.yaml).
type Reglas struct {
	Layers          map[string][]string       `yaml:"layers"`
	AllowedEdges    map[string][]string       `yaml:"allowed_edges"`
	RestrictedEdges map[string]RestrictedEdge `yaml:"restricted_edges"`
	SharedPackages  []string                  `yaml:"shared_packages"`
	CompositionRoot CompositionRoot           `yaml:"composition_root"`
}

// RestrictedEdge describe una arista permitida pero limitada a ciertas
// operaciones y consumidores (DEC-051).
type RestrictedEdge struct {
	Operations []string `yaml:"operations"`
	Consumers  []string `yaml:"consumers"`
}

// CompositionRoot describe la raíz de composición y los módulos que compone.
type CompositionRoot struct {
	Module          string   `yaml:"module"`
	RequiresExactly []string `yaml:"requires_exactly"`
}

func cargarReglas() (*Reglas, error) {
	var r Reglas
	if err := yaml.Unmarshal(reglasYAML, &r); err != nil {
		return nil, fmt.Errorf("parsear reglas.yaml: %w", err)
	}
	return &r, nil
}

// servicios devuelve el conjunto de todos los nombres de servicio conocidos
// (unión de las capas).
func (r *Reglas) servicios() map[string]bool {
	s := map[string]bool{}
	for _, nombres := range r.Layers {
		for _, n := range nombres {
			s[n] = true
		}
	}
	return s
}

// aristasPermitidas devuelve el conjunto de servicios que `servicio` puede
// importar (clientes generados), según allowed_edges.
func (r *Reglas) aristasPermitidas(servicio string) map[string]bool {
	m := map[string]bool{}
	for _, dst := range r.AllowedEdges[servicio] {
		m[dst] = true
	}
	return m
}

// paquetesCompartidos devuelve el conjunto de paquetes de api-client-go
// importables por cualquier servicio (shared_packages). No son aristas: se
// excluyen de la verificación del grafo.
func (r *Reglas) paquetesCompartidos() map[string]bool {
	m := map[string]bool{}
	for _, p := range r.SharedPackages {
		m[p] = true
	}
	return m
}
