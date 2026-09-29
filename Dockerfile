# YardMaster image: switchyard-server built from a pinned upstream commit, plus the
# YardMaster binary with its web UI embedded.

# Upgrading Switchyard is a deliberate change of both values, tested before release.
ARG SWITCHYARD_TAG=v0.3.0
ARG SWITCHYARD_COMMIT=336196f6fbfc97ddc71c1700f6092e564e9f23c2

# ---- switchyard-server, the same way upstream's own Dockerfile builds it ----
FROM rust:1.96.1-bookworm AS switchyard
ARG SWITCHYARD_TAG
ARG SWITCHYARD_COMMIT
RUN git clone --depth 1 --branch "${SWITCHYARD_TAG}" https://github.com/NVIDIA-NeMo/Switchyard /src \
    && test "$(git -C /src rev-parse HEAD)" = "${SWITCHYARD_COMMIT}" \
    || { echo "tag ${SWITCHYARD_TAG} does not point at ${SWITCHYARD_COMMIT}" >&2; exit 1; }
WORKDIR /src
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/src/target \
    cargo build --locked --release -p switchyard-server \
    && cp target/release/switchyard-server /switchyard-server

# ---- web UI ----
FROM node:22-bookworm-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- yardmaster ----
FROM golang:1.26-bookworm AS yardmaster
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /web/dist ./internal/httpapi/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /yardmaster ./cmd/yardmaster

# ---- final image ----
FROM debian:bookworm-slim
ARG SWITCHYARD_TAG
RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 yardmaster \
    && useradd --uid 1000 --gid 1000 --home-dir /data --no-create-home yardmaster \
    && mkdir -p /data && chown 1000:1000 /data
COPY --from=switchyard /switchyard-server /usr/local/bin/switchyard-server
COPY --from=switchyard /src/LICENSE /src/NOTICE /usr/share/doc/switchyard/
COPY --from=yardmaster /yardmaster /usr/local/bin/yardmaster
ENV HOME=/data \
    YARDMASTER_DATA=/data \
    YARDMASTER_SWITCHYARD_VERSION=${SWITCHYARD_TAG}
USER 1000:1000
VOLUME /data
EXPOSE 8080 8443
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
    CMD ["yardmaster", "healthcheck"]
ENTRYPOINT ["yardmaster"]
CMD ["serve"]
