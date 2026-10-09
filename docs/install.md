# Installing and running YardMaster

YardMaster is software made of one Go binary, with the web UI built in, plus Switchyard's
`switchyard-server`, which YardMaster starts and supervises. It's delivered as a Docker image that
holds both. You can also build and run it [without Docker](#without-docker).

## Get the image

Each release is published as `ghcr.io/bricke/yardmaster` for `linux/amd64` and `linux/arm64`,
tagged with its version (`0.2.0`), its minor line (`0.2`) and `latest`; Docker picks the right one
for the host. On amd64 it needs a CPU with AVX2 (Intel Haswell, AMD Zen or newer), on arm64 a
Neoverse N1 or newer (AWS Graviton 2, Ampere Altra, NVIDIA Grace…), as Switchyard's own builds do.
Pin a version so an upgrade happens only when you choose it:

```bash
docker pull ghcr.io/bricke/yardmaster:0.2
```

To build it yourself instead, from a release tag or `main`: Switchyard publishes no image, so the
build compiles it from a pinned release, and the first build takes several minutes.

```bash
make image            # tags yardmaster:latest, and the YardMaster and Switchyard versions
```

## Run it

Map the ports to the same numbers on the host, because YardMaster includes them in the addresses
it gives people.

```bash
docker run -d --name yardmaster --restart unless-stopped \
  -p 8080:8080 -p 8443:8443 \
  -v yardmaster-data:/data \
  -e YARDMASTER_SECRET_KEY="$(openssl rand -base64 32)" \
  ghcr.io/bricke/yardmaster:0.2
docker logs yardmaster        # shows a one-time setup code
```

With an image you built, use `yardmaster` as the image name.

Store the `YARDMASTER_SECRET_KEY` value somewhere safe. It encrypts the provider keys you enter;
if you lose it, they can't be read back. A random key like the one above is best. A passphrase
also works: YardMaster stretches it with Argon2id, which makes guessing slow, but a short or
common one is still weak.

## Without Docker

The Docker image is the tested way to run YardMaster. Outside it, you need three things:

1. **The YardMaster binary.** `make build` builds the UI and then the Go binary, which embeds the
   UI. It needs Go 1.26 and Node 22.
2. **`switchyard-server`.** Build it from the Switchyard source, at the release and commit in the
   `Dockerfile` (`SWITCHYARD_TAG`, `SWITCHYARD_COMMIT`), with `cargo build --locked --release -p switchyard-server`. Put it on the `PATH`,
   or point `YARDMASTER_SWITCHYARD_BIN` at it. The image pins an exact Switchyard commit, and other
   versions are untested.
3. **A writable data folder.** The default is `/data`. Set `YARDMASTER_DATA` to use another one.

```bash
YARDMASTER_DATA=/var/lib/yardmaster \
YARDMASTER_SECRET_KEY="…" \
./yardmaster
```

To keep it running, use your service manager (systemd, for example) and set the environment
variables there. YardMaster restarts `switchyard-server` itself when you apply a config. Running
without Docker hasn't been tested outside development.

## First sign-in

1. Open `http://<host>:8080` and enter the setup code from the container log.
2. Choose the admin password.
3. Follow **Setup** to add providers, models and routes. The [user guide](guide.md) walks through
   each step.

To skip the setup code, set `YARDMASTER_ADMIN_PASSWORD` on the first start.

If you forget the admin password, run:

```bash
docker exec -it yardmaster yardmaster reset-password
```

## HTTPS

In **Settings**, give YardMaster the DNS name people use to reach it (or set
`YARDMASTER_PUBLIC_HOST`). YardMaster then creates its own certificate authority and serves HTTPS
on port 8443. The **Connect an agent** page links to the CA certificate so people can download it
and have their tools trust it.

To use your own certificate instead, put `custom.crt` and `custom.key` in `/data/tls/`.

## Behind another proxy

Set `YARDMASTER_AUTH=proxy` when a portal in front of YardMaster handles sign-in and TLS. The
`YARDMASTER_PROXY_*` variables tell YardMaster which headers and addresses to trust. See
[Identity](architecture.md#identity) and [Configuration](architecture.md#configuration-env-vars) in
the architecture.

If the proxy reaches YardMaster under a different name than the one in the browser (for example
`yardmaster:8080` on the Docker network while people open `https://ai.example.com`), it must send
the browser's host in `X-Forwarded-Host`, and `X-Forwarded-Proto: https` when it serves HTTPS.
Otherwise every change made in the UI is refused with "it didn't come from this site", and
**Connect an agent** shows the internal address instead of the one your coworkers use. Caddy and
Traefik send both by default; with nginx, add `proxy_set_header X-Forwarded-Host $http_host;`
(`$http_host` keeps the port, which the check needs) and
`proxy_set_header X-Forwarded-Proto $scheme;`. The audit log records the client's address from
`X-Forwarded-For`. YardMaster believes these headers only from the trusted proxy: a request carrying
the shared secret, or one from an address in `YARDMASTER_PROXY_ADDRESSES`.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `YARDMASTER_ADMIN_PASSWORD` | | Creates the admin with this password instead of asking for the setup code |
| `YARDMASTER_SECRET_KEY` | | Encrypts provider keys at rest (recommended) |
| `YARDMASTER_PUBLIC_HOST` | | The DNS name for HTTPS, instead of setting it in the UI |
| `YARDMASTER_TLS` | `auto` | Set `off` to serve plain HTTP only |
| `YARDMASTER_SHUTDOWN_TIMEOUT` | `30s` | How long in-flight requests get to finish when the router restarts |
| `YARDMASTER_USAGE_RETENTION_DAYS` | `90` | How long per-request usage rows are kept; monthly totals are kept |
| `YARDMASTER_AUDIT_RETENTION_DAYS` | `365` | How long the audit log is kept |
| `YARDMASTER_AUTH` | `builtin` | Set `proxy` to run behind another proxy that signs people in |
| Provider keys, e.g. `OPENROUTER_API_KEY` | | Used as is, and shown read-only in the UI |

The [architecture](architecture.md#configuration-env-vars) lists the rest, including the ports and
the proxy settings.

## Backups

Everything YardMaster keeps is in `/data`: the database, the provider keys, the Switchyard config
and its history, and the certificates. Back up that volume. If you set `YARDMASTER_SECRET_KEY`,
store it separately. `/data` never contains prompts or responses.
