# Local development helpers. The Docker image doesn't need any of these.

SWITCHYARD_TAG    ?= v0.3.0
VERSION           ?= $(patsubst v%,%,$(shell git describe --tags --always --dirty 2>/dev/null || echo dev))
IMAGE             ?= yardmaster:$(VERSION)-sy$(SWITCHYARD_TAG:v%=%)

.PHONY: web build test check image clean

web:            ## build the UI into the Go embed folder
	cd web && npm ci && npm run build
	rm -rf internal/httpapi/dist/assets internal/httpapi/dist/index.html internal/httpapi/dist/favicon.svg
	cp -r web/dist/. internal/httpapi/dist/

build: web      ## build the yardmaster binary
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o yardmaster ./cmd/yardmaster

test:           ## run the Go tests
	go test ./...

check:          ## every check to pass before committing
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w on the files above"; exit 1; }
	go test ./...
	cd web && npm run build

image:          ## build the Docker image, tagged with its versions and as yardmaster:latest
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) -t yardmaster:latest .
	@echo "built $(IMAGE)"

clean:
	rm -rf yardmaster web/dist internal/httpapi/dist/assets internal/httpapi/dist/index.html internal/httpapi/dist/favicon.svg
