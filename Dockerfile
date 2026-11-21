# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.27-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/lanesiege ./cmd/lanesiege

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lanesiege /lanesiege

ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot

# Rooms live in memory; a single container is one self-contained host.
ENTRYPOINT ["/lanesiege"]
