# syntax=docker/dockerfile:1

# Plantilla de imagen para los repos con raíz de composición (pf-svc-core,
# pf-svc-mensajeria, pf-svc-realtime) y cualquier cmd/api. NO es un Dockerfile
# por módulo: cada repo con raíz COPIA esta plantilla y pasa CMD_PATH.
#
# CMD_PATH es obligatorio y sin valor por defecto: apunta al paquete main a
# compilar (p. ej. "./cmd/core", "./cmd/mensajeria", "./cmd/api").
#
# Repos privados (handoff §1.1): como AI27-Tech/* es privado, la resolución de
# módulos necesita un token de GitHub. Se inyecta como SECRETO de BuildKit, no
# como ARG ni capa persistente:
#   DOCKER_BUILDKIT=1 docker build \
#     --secret id=gh_token,env=GH_TOKEN \
#     --build-arg CMD_PATH=./cmd/core -t ai27/core .

# ---- Build stage ----
FROM golang:1.27-alpine AS build

# Obligatorio, sin default: el build falla si no se pasa.
ARG CMD_PATH
# GOPRIVATE cubre los módulos privados de la organización.
ARG GOPRIVATE=github.com/AI27-Tech/*

RUN apk add --no-cache git

WORKDIR /src

# Capa de dependencias (cacheable). El token privado se monta como secreto solo
# durante los comandos que resuelven módulos, nunca queda en la imagen.
COPY go.mod go.sum ./
RUN --mount=type=secret,id=gh_token \
    sh -c 'if [ -f /run/secrets/gh_token ]; then \
      git config --global url."https://x-access-token:$(cat /run/secrets/gh_token)@github.com/".insteadOf "https://github.com/"; \
    fi' && \
    GOPRIVATE="${GOPRIVATE}" go mod download

# Código fuente.
COPY . .

# Compila el binario desde la raíz de composición indicada por CMD_PATH.
RUN --mount=type=secret,id=gh_token \
    test -n "${CMD_PATH}" || (echo "ERROR: falta --build-arg CMD_PATH" && exit 1) && \
    CGO_ENABLED=0 GOOS=linux GOPRIVATE="${GOPRIVATE}" \
      go build -ldflags="-s -w" -o /bin/server "${CMD_PATH}"

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /bin/server /server

EXPOSE 8080

ENTRYPOINT ["/server"]
