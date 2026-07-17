# --- build ---
FROM golang:1.23-alpine AS build

WORKDIR /src

# Závislosti zvlášť, ať se cachují nezávisle na zdrojácích.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Driver modernc.org/sqlite je čisté Go, takže CGO není potřeba a
# binárka je statická.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/kdy-hrajeme .

# --- run ---
FROM alpine:3.20

# wget kvůli healthchecku, su-exec kvůli zahození roota v entrypointu.
RUN apk add --no-cache ca-certificates tzdata wget su-exec

RUN adduser -D -u 10001 app
RUN mkdir -p /data && chown app:app /data

COPY --from=build /out/kdy-hrajeme /usr/local/bin/kdy-hrajeme
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Schválně tu není USER app: kontejner musí nastartovat jako root, aby
# šlo srovnat vlastnictví připojeného volume. Entrypoint pak spustí
# aplikaci pod uživatelem app.
WORKDIR /data
VOLUME /data

ENV PORT=8080
ENV DB_PATH=/data/kdyhrajeme.db
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/kdy-hrajeme"]
