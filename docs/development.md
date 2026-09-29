# Development

You need Go 1.26 and Node 22. `switchyard-server` must be on your `PATH`, or set
`YARDMASTER_SWITCHYARD_BIN` to it. To build one, run `docker build --target switchyard .`.

```bash
make test                                   # Go tests
make build                                  # UI + binary
YARDMASTER_DATA=./data YARDMASTER_ADMIN_PASSWORD=devpassword1 ./yardmaster
cd web && npm run dev                       # UI with live reload, proxied to :8080
```

Before committing, run `make check`: `go vet ./...`, `gofmt -l .`, `go test ./...` and
`npm run build` in `web/`, which must finish with no warnings. For UI changes, also run the app and
look at the page.

GitHub Actions runs the same checks on every pull request and on `main`
(`.github/workflows/ci.yml`), with the Go tests under the race detector (`go test -race ./...`). It
also builds the Docker image and checks that a container from it answers on `/health`. The image
build compiles `switchyard-server` from source, so it takes a while the first time; later runs reuse
the build cache.

The supervisor tests don't need `switchyard-server`: the test binary stands in for it, and the config
file's contents tell it how to behave (healthy, crashing, ignoring SIGTERM…). See
`internal/supervisor/supervisor_test.go`. `go test -short` skips the one test that waits for the
shutdown timeout.

The [architecture](architecture.md) covers the code layout and the reasoning behind it.
