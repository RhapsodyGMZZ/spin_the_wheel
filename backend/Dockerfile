# syntax=docker/dockerfile:1

# ---- Étage de compilation --------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dépendances d'abord : cette couche est mise en cache tant que go.mod/go.sum
# ne bougent pas.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Binaire statique, sans table de symboles, chemins de build neutralisés.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -buildvcs=false \
        -ldflags="-s -w" \
        -o /out/spinwheel ./cmd/server

# Arborescence de données pré-créée avec le bon propriétaire : Docker s'en sert
# pour initialiser les droits du volume nommé monté sur /data/images.
RUN mkdir -p /out/data/images

# ---- Image finale ----------------------------------------------------------
# distroless static : pas de shell, pas de gestionnaire de paquets, pas de libc.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/spinwheel /usr/local/bin/spinwheel
COPY --from=build --chown=nonroot:nonroot /out/data /data

USER nonroot:nonroot
WORKDIR /

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/spinwheel"]
