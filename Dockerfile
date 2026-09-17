# syntax=docker/dockerfile:1

# Multi-stage build parametrizado por raíz de composición.
# ARG ROOT define qué binario se compila (e.g. "core", "mensajeria", "realtime").
# No se crea un Dockerfile por módulo; la raíz de composición define el binario.

# ---- Build stage ----
FROM golang:1.27-alpine AS build

ARG ROOT
ARG MODULE_PATH=github.com/AI27-Tech/pf-pkg-platform-go

WORKDIR /src

# Copy go.mod and go.sum for dependency caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy all source code.
COPY . .

# Build the binary from the composition root.
# The ROOT arg should match a directory under cmd/ (e.g. "cmd/core").
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/server ./cmd/${ROOT}/api

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /bin/server /server

EXPOSE 8080

ENTRYPOINT ["/server"]
