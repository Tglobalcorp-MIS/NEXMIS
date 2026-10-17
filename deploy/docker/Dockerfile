# syntax=docker/dockerfile:1.7
# NEXMIS server image: web UI + server + bundled Windows agent binaries.

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
RUN apk add --no-cache bash coreutils
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN SKIP_WEB=1 VERSION=${VERSION} OUT=/out scripts/build.sh

FROM alpine:3.22
ARG TARGETARCH
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S nexmis && adduser -S -G nexmis -h /var/lib/nexmis nexmis \
 && mkdir -p /var/lib/nexmis && chown nexmis:nexmis /var/lib/nexmis
COPY --from=build /out/nexmis-server-linux-${TARGETARCH} /usr/local/bin/nexmis-server
COPY --from=build /out/agent /opt/nexmis/agent
ENV NEXMIS_LISTEN=:8080 \
    NEXMIS_DATA_DIR=/var/lib/nexmis \
    NEXMIS_AGENT_DIR=/opt/nexmis/agent \
    NEXMIS_LOG_FORMAT=json \
    TZ=Asia/Taipei
USER nexmis
VOLUME /var/lib/nexmis
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/nexmis-server"]
