# syntax=docker/dockerfile:1

## --- Build stage ---
FROM golang:1.26-bookworm AS build
WORKDIR /src

ARG VERSION=dev

# Cache module downloads separately from source changes.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=${VERSION}" -o /out/billtracker ./cmd/server

## --- Runtime stage ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app

COPY --from=build /out/billtracker ./billtracker

# distroless "nonroot" image already runs as a non-root UID (65532); Kubernetes
# manifests additionally pin this via securityContext for clarity/defense-in-depth.
USER nonroot:nonroot

EXPOSE 8090

ENTRYPOINT ["/app/billtracker"]
