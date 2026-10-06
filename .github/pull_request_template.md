<!--
Plantilla de Pull Request de pf-pkg-platform-go (plantilla de servicio compartida
de AI27). Es una librería SIN dominio: no conoce `viaje`, `custodio`, etc. Si un
cambio empieza a necesitar saber qué es un sustantivo de negocio, no pertenece
aquí. Rellena lo que aplique y borra el resto.
-->

## Resumen

<!-- Qué hace este PR y por qué. Una o dos frases. -->

## Contexto

- Issue / spec / historia técnica (E0-*):
- ADR / decisión afectada (DEC-xxx):

## Tipo de cambio

- [ ] Feature (nuevo paquete o capacidad reutilizable)
- [ ] Bugfix
- [ ] Refactor (sin cambio de comportamiento)
- [ ] Documentación
- [ ] Tooling / build / CI
- [ ] Release (nuevo tag)
- [ ] Otro:

## Cambios

<!-- Lista concreta por paquete (worker, bus, inbox, outbox, auth, observability, server, tools/arch-test, ...). -->

-

## Invariantes de la plataforma

- [ ] No afecta ninguna invariante documentada.
- [ ] **Sin dominio** — ningún nombre de evento, tema, cola, esquema ni rol de negocio en el código; el registro y los nombres se inyectan.
- [ ] **Puertos agnósticos** — `bus`/`worker`/`inbox` siguen sin conocer el transporte; ningún adaptador de broker concreto ni SDK de AWS (DEC-062).
- [ ] **`auth`** — el verificador no conoce al proveedor (URL del JWKS y emisor se inyectan); se conserva el fallo cerrado y el refresco proactivo (DEC-056).
- [ ] **arch-test** — si cambia `reglas.yaml` o el grafo, se actualizan sus pruebas (incluidos los casos negativos).

## Compatibilidad

<!-- Esta librería la consumen todos los pf-svc-* por versión exacta. -->

- [ ] Cambio compatible hacia atrás; o
- [ ] Cambio incompatible — documentado aquí y reflejado en la versión del tag.

## Cómo se probó

- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `go test -race ./...`
- [ ] Pruebas de contrato del puerto (`bus/bustest`) donde aplique
- [ ] Pruebas de `tools/arch-test` (incluidos los negativos)
- [ ] `gofmt` limpio

Notas de la verificación:

## CHANGELOG

- [ ] Agregué la entrada en `CHANGELOG.md` (fecha+hora, resumen, cambios y archivos; la más reciente al inicio).

## Checklist

- [ ] Identificadores en inglés salvo sustantivos de dominio en español; comentarios, README y commits en español (México).
- [ ] No commiteé binarios ni artefactos generados.
- [ ] Sin secretos, DSN ni URLs hardcodeadas.
