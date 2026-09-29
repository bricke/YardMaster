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

The [architecture](architecture.md) covers the code layout and the reasoning behind it.
