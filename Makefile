BIN := locally

# Used internally.  Users should pass GOOS and/or GOARCH.
OS   := $(if $(GOOS),$(GOOS),$(shell go env GOOS))
ARCH := $(if $(GOARCH),$(GOARCH),$(shell go env GOARCH))

build:
	@go build -o $(BIN)

run:
	@go run . expose

test:
	@go test ./pkg/... ./cmd/...

.PHONY: build run test