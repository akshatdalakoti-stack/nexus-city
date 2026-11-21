# One Dockerfile for every Go service; pick the binary with --build-arg SERVICE=sim|gateway|headless.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE=gateway
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/app ./cmd/${SERVICE}

# Distroless: no shell, runs as non-root. Health checks use `/app healthcheck`.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
