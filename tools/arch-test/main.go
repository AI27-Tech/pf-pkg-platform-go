// Command arch-test verifica el grafo de arquitectura de AI27 (ADR 17, E0-T6).
// Cada repo pf-svc-* lo corre en su CI:
//
//	go run github.com/AI27-Tech/pf-pkg-platform-go/tools/arch-test@<version>
//
// El grafo vive una sola vez, embebido en reglas.yaml. arch-test identifica el
// repo actual por la ruta de su módulo (pf-svc-<x>) y verifica:
//
//	(a) ningún go.mod de pf-svc-* requiere otro pf-svc-* salvo pf-svc-core;
//	(b) cada módulo solo importa los clientes generados de sus aristas declaradas;
//	(c) los imports reales con `go list -deps`;
//	(d) que el grafo declarado es acíclico;
//	(e) la regla de pf-svc-core (importa EXACTAMENTE los módulos que compone).
//
// Un módulo desconocido (ruta que no mapea a un servicio del grafo) falla.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "arch-test:", err)
		os.Exit(2)
	}

	reglas, err := cargarReglas()
	if err != nil {
		fmt.Fprintln(os.Stderr, "arch-test: cargar reglas:", err)
		os.Exit(2)
	}

	problemas, err := verificar(abs, reglas)
	if err != nil {
		fmt.Fprintln(os.Stderr, "arch-test:", err)
		os.Exit(2)
	}

	if len(problemas) > 0 {
		fmt.Fprintln(os.Stderr, "arch-test: violaciones de arquitectura:")
		for _, p := range problemas {
			fmt.Fprintln(os.Stderr, "  -", p)
		}
		os.Exit(1)
	}

	fmt.Println("arch-test: OK")
}
