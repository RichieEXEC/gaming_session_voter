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

# Kvůli HTTPS voláním a rozumným časovým zónám.
RUN apk add --no-cache ca-certificates tzdata wget

# Neběžet pod rootem.
RUN adduser -D -u 10001 app
RUN mkdir -p /data && chown app:app /data

COPY --from=build /out/kdy-hrajeme /usr/local/bin/kdy-hrajeme

USER app
WORKDIR /data
VOLUME /data

ENV PORT=8080
ENV DB_PATH=/data/kdyhrajeme.db
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/kdy-hrajeme"]
