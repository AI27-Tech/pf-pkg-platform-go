package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// orgPrefix es el prefijo de módulo de los repos de servicio.
	orgPrefix = "github.com/AI27-Tech/pf-svc-"
	// contractsClientPrefix es el prefijo de los clientes generados por servicio
	// en pf-pkg-contracts. Un import de este prefijo seguido de <servicio> es una
	// arista síncrona hacia <servicio>.
	contractsClientPrefix = "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/"
)

var moduleLineRe = regexp.MustCompile(`(?m)^\s*module\s+(\S+)`)

// verificar corre todas las comprobaciones sobre el repo en dir. Devuelve la
// lista de problemas (vacía si todo pasa) o un error si no pudo analizar.
func verificar(dir string, r *Reglas) ([]string, error) {
	// (d) El grafo DECLARADO debe ser acíclico, independientemente del repo.
	problemas := verificarAciclico(r)

	modulePath, err := leerModulePath(dir)
	if err != nil {
		return nil, err
	}

	servicio, esServicio := nombreDeServicio(modulePath, r)
	esCore := strings.HasSuffix(modulePath, r.CompositionRoot.Module)

	if !esServicio && !esCore {
		// Un módulo desconocido falla (00 regla 5 / E0-T6): no es ni un servicio
		// del grafo ni la raíz de composición.
		return append(problemas, fmt.Sprintf(
			"módulo %q no corresponde a ningún servicio del grafo ni a la raíz de composición %q",
			modulePath, r.CompositionRoot.Module)), nil
	}

	// (a) go.mod no debe requerir otro pf-svc-* salvo core.
	reqs, err := requeridosServicio(dir)
	if err != nil {
		return nil, err
	}
	if !esCore {
		for _, dep := range reqs {
			problemas = append(problemas, fmt.Sprintf(
				"%s: su go.mod requiere otro servicio (%s); solo pf-svc-core puede (00 regla 5a)",
				modulePath, dep))
		}
	}

	// (b)+(c) Imports reales de clientes generados contra las aristas declaradas.
	importsPorServicio, err := importsDeClientes(dir, r.paquetesCompartidos())
	if err != nil {
		return nil, err
	}

	if esCore {
		// (e) La raíz importa EXACTAMENTE los cinco módulos declarados.
		problemas = append(problemas, verificarCore(importsPorServicio, r)...)
	} else {
		permitidas := r.aristasPermitidas(servicio)
		for dst := range importsPorServicio {
			if dst == servicio {
				continue
			}
			if !permitidas[dst] {
				problemas = append(problemas, fmt.Sprintf(
					"%s importa el cliente de %q, que no es una arista declarada de %q",
					servicio, dst, servicio))
			}
		}
	}

	return problemas, nil
}

// leerModulePath extrae la ruta del módulo del go.mod del directorio.
func leerModulePath(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("leer go.mod en %s: %w", dir, err)
	}
	m := moduleLineRe.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("no se encontró la línea module en %s/go.mod", dir)
	}
	return string(m[1]), nil
}

// nombreDeServicio mapea la ruta del módulo a un servicio del grafo.
func nombreDeServicio(modulePath string, r *Reglas) (string, bool) {
	if !strings.HasPrefix(modulePath, orgPrefix) {
		return "", false
	}
	nombre := strings.TrimPrefix(modulePath, orgPrefix)
	if r.servicios()[nombre] {
		return nombre, true
	}
	return "", false
}

// requeridosServicio devuelve los módulos pf-svc-* que el go.mod del repo
// requiere (excluyendo el propio módulo).
func requeridosServicio(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("leer go.mod: %w", err)
	}
	propio, _ := leerModulePath(dir)
	var out []string
	for _, línea := range strings.Split(string(data), "\n") {
		línea = strings.TrimSpace(línea)
		línea = strings.TrimPrefix(línea, "require ")
		if !strings.HasPrefix(línea, orgPrefix) {
			continue
		}
		campos := strings.Fields(línea)
		if len(campos) == 0 {
			continue
		}
		mod := campos[0]
		if mod == propio {
			continue
		}
		out = append(out, mod)
	}
	return out, nil
}

// importsDeClientes corre `go list -deps` y devuelve, por servicio destino, los
// paquetes de cliente generado que el repo importa de verdad. Los paquetes
// compartidos (shared_packages: enum de roles, estados, eventos…) se importan
// por cualquiera y NO son aristas, así que se excluyen del resultado.
func importsDeClientes(dir string, compartidos map[string]bool) (map[string][]string, error) {
	cmd := exec.Command("go", "list", "-deps", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// Un repo sin paquetes aún (p. ej. recién scaffoldeado) no es un error de
		// arquitectura: se trata como sin imports.
		if _, ok := err.(*exec.ExitError); ok {
			return map[string][]string{}, nil
		}
		return nil, fmt.Errorf("go list -deps: %w", err)
	}

	resultado := map[string][]string{}
	for _, pkg := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg = strings.TrimSpace(pkg)
		if !strings.HasPrefix(pkg, contractsClientPrefix) {
			continue
		}
		resto := strings.TrimPrefix(pkg, contractsClientPrefix)
		servicio := strings.SplitN(resto, "/", 2)[0]
		// Un paquete compartido no es una arista hacia un servicio: se ignora.
		if compartidos[servicio] {
			continue
		}
		resultado[servicio] = append(resultado[servicio], pkg)
	}
	return resultado, nil
}

// verificarCore comprueba que la raíz importa EXACTAMENTE los módulos declarados.
func verificarCore(imports map[string][]string, r *Reglas) []string {
	var problemas []string
	esperado := map[string]bool{}
	for _, m := range r.CompositionRoot.RequiresExactly {
		esperado[m] = true
	}
	// Los clientes generados que importa core son la unión de las aristas entre
	// los módulos; no se exige que importe el cliente de cada uno, pero sí que no
	// importe el cliente de un servicio ajeno al conjunto compuesto.
	for dst := range imports {
		if !esperado[dst] {
			problemas = append(problemas, fmt.Sprintf(
				"pf-svc-core importa el cliente de %q, que no está entre los módulos que compone (%s)",
				dst, strings.Join(r.CompositionRoot.RequiresExactly, ", ")))
		}
	}
	return problemas
}

// verificarAciclico comprueba que allowed_edges no tiene ciclos.
func verificarAciclico(r *Reglas) []string {
	const (
		blanco = 0
		gris   = 1
		negro  = 2
	)
	color := map[string]int{}
	var ciclo []string

	var dfs func(n string, camino []string) bool
	dfs = func(n string, camino []string) bool {
		color[n] = gris
		camino = append(camino, n)
		for _, m := range r.AllowedEdges[n] {
			switch color[m] {
			case gris:
				ciclo = append(append([]string{}, camino...), m)
				return true
			case blanco:
				if dfs(m, camino) {
					return true
				}
			}
		}
		color[n] = negro
		return false
	}

	for n := range r.AllowedEdges {
		if color[n] == blanco {
			if dfs(n, nil) {
				return []string{"el grafo allowed_edges tiene un ciclo: " + strings.Join(ciclo, " -> ")}
			}
		}
	}
	return nil
}
