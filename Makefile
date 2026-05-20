.PHONY: all proto daemon panel bridge clean test lint fmt install

GO        ?= go
GOFLAGS   ?= -trimpath -ldflags="-s -w"
PROTOC    ?= protoc
PROTO_GO  := --go_out=. --go_opt=paths=source_relative \
             --go-grpc_out=. --go-grpc_opt=paths=source_relative

PROTO_FILES := $(shell find proto -name '*.proto' 2>/dev/null)

all: proto daemon panel bridge

proto:
	@echo ">> generating protobuf"
	@$(PROTOC) -I proto $(PROTO_GO) $(PROTO_FILES)

daemon:
	@echo ">> building aether-daemon"
	@cd daemon && $(GO) build $(GOFLAGS) -o ../bin/aether-daemon ./cmd/aether-daemon

panel:
	@echo ">> building aether-panel"
	@cd panel && $(GO) build $(GOFLAGS) -o ../bin/aether-panel ./cmd/aether-panel

bridge:
	@echo ">> building bridge plugins"
	@cd bridge && ./gradlew shadowJar

test:
	@cd daemon && $(GO) test ./...
	@cd panel  && $(GO) test ./...
	@cd pkg    && $(GO) test ./...

lint:
	@cd daemon && $(GO) vet ./...
	@cd panel  && $(GO) vet ./...
	@cd pkg    && $(GO) vet ./...

fmt:
	@cd daemon && $(GO) fmt ./...
	@cd panel  && $(GO) fmt ./...
	@cd pkg    && $(GO) fmt ./...

install: all
	@install -m0755 bin/aether-daemon /usr/local/bin/aether-daemon
	@install -m0755 bin/aether-panel  /usr/local/bin/aether-panel

clean:
	@rm -rf bin/ dist/
	@find . -name '*.pb.go' -delete
	@find . -name '*_grpc.pb.go' -delete
