# syntax=docker/dockerfile:1

# --- deps: cache module downloads separately from source changes ---
FROM golang:1.25-bookworm AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# --- build: compile the forge CLI ---
FROM deps AS build
COPY . .
RUN CGO_ENABLED=0 go build -o /out/forge ./cmd/forge

# --- runtime: minimal final image, binary only ---
FROM gcr.io/distroless/base-debian12:nonroot AS runtime
COPY --from=build /out/forge /usr/local/bin/forge
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/forge"]
CMD ["--help"]
