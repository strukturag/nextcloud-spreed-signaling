all: build

GO := $(shell which go)
GOPATH := $(shell "$(GO)" env GOPATH)
GODIR := $(shell dirname "$(GO)")
GOFMT := "$(GODIR)/gofmt"
GOOS ?= linux
GOARCH ?= amd64
GOVERSION := $(shell "$(GO)" env GOVERSION | sed "s|go||" )
TMPDIR := $(CURDIR)/tmp
BINDIR := $(CURDIR)/bin
VENDORDIR := "$(CURDIR)/vendor"
VERSION := $(shell "$(CURDIR)/scripts/get-version.sh")
TARVERSION := $(shell "$(CURDIR)/scripts/get-version.sh" --tar)
PACKAGENAME := github.com/strukturag/nextcloud-spreed-signaling
GRPC_PROTO_FILES := $(basename $(wildcard grpc_*.proto))
PROTOBUF_VERSION := $(shell grep google.golang.org/protobuf go.mod | xargs | cut -d ' ' -f 2)
PROTO_FILES := $(filter-out $(GRPC_PROTO_FILES),$(basename $(wildcard *.proto)))
PROTO_GO_FILES := $(addsuffix .pb.go,$(PROTO_FILES))
GRPC_PROTO_GO_FILES := $(addsuffix .pb.go,$(GRPC_PROTO_FILES)) $(addsuffix _grpc.pb.go,$(GRPC_PROTO_FILES))
TEST_GO_FILES := $(wildcard *_test.go))
EASYJSON_FILES := $(filter-out $(TEST_GO_FILES),$(wildcard api*.go api/signaling.go */api.go talk/ocs.go))
EASYJSON_GO_FILES := $(patsubst %.go,%_easyjson.go,$(EASYJSON_FILES))
COMMON_GO_FILES := $(filter-out geoip/continentmap.go $(PROTO_GO_FILES) $(GRPC_PROTO_GO_FILES) $(EASYJSON_GO_FILES) $(TEST_GO_FILES),$(wildcard *.go))
CLIENT_TEST_GO_FILES := $(wildcard client/*_test.go))
CLIENT_GO_FILES := $(filter-out $(CLIENT_TEST_GO_FILES),$(wildcard client/*.go))
SERVER_TEST_GO_FILES := $(wildcard server/*_test.go))
SERVER_GO_FILES := $(filter-out $(SERVER_TEST_GO_FILES),$(wildcard server/*.go))
PROXY_TEST_GO_FILES := $(wildcard proxy/*_test.go))
PROXY_GO_FILES := $(filter-out $(PROXY_TEST_GO_FILES),$(wildcard proxy/*.go))

ifneq ($(VERSION),)
INTERNALLDFLAGS := -X main.version=$(VERSION)
else
INTERNALLDFLAGS :=
endif

ifneq ($(RACE),)
BUILDARGS := -race
else
BUILDARGS :=
endif

ifneq ($(CI),)
TESTARGS := -race
else
TESTARGS :=
endif

ifeq ($(TIMEOUT),)
TIMEOUT := 60s
endif

ifeq ($(BENCHMARK),)
BENCHMARK := .
endif

ifneq ($(TEST),)
TESTARGS := $(TESTARGS) -run "$(TEST)"
endif

ifneq ($(COUNT),)
TESTARGS := $(TESTARGS) -count $(COUNT)
endif

ifneq ($(PARALLEL),)
TESTARGS := $(TESTARGS) -parallel $(PARALLEL)
endif

ifneq ($(VERBOSE),)
TESTARGS := $(TESTARGS) -v
endif

ifeq ($(GOARCH), amd64)
GOPATHBIN := $(GOPATH)/bin
else
GOPATHBIN := $(GOPATH)/bin/$(GOOS)_$(GOARCH)
endif

hook:
	[ ! -d "$(CURDIR)/.git/hooks" ] || ln -sf "$(CURDIR)/scripts/pre-commit.hook" "$(CURDIR)/.git/hooks/pre-commit"

$(GOPATHBIN)/easyjson: go.mod go.sum | $(TMPDIR)
	$(GO) install github.com/mailru/easyjson/...

$(GOPATHBIN)/protoc-gen-go: go.mod go.sum
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOBUF_VERSION)

$(GOPATHBIN)/protoc-gen-go-grpc: go.mod go.sum
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc

$(GOPATHBIN)/checklocks: go.mod go.sum
	$(GO) install gvisor.dev/gvisor/tools/checklocks/cmd/checklocks@go

geoip/continentmap.go:
	$(CURDIR)/scripts/get_continent_map.py $@

check-continentmap:
	set -e ;\
	TMP=$$(mktemp -d) ;\
	echo Make sure to remove $$TMP on error ;\
	$(CURDIR)/scripts/get_continent_map.py $$TMP/continentmap.go ;\
	diff -u geoip/continentmap.go $$TMP/continentmap.go ;\
	rm -rf $$TMP

get:
	$(GO) get $(PACKAGE)

fmt: hook | $(PROTO_GO_FILES)
	$(GOFMT) -s -w *.go client proxy server

vet:
	GOEXPERIMENT=synctest $(GO) vet ./...

test: vet
	GOEXPERIMENT=synctest $(GO) test -timeout $(TIMEOUT) $(TESTARGS) ./...

benchmark:
	GOEXPERIMENT=synctest $(GO) test -bench=$(BENCHMARK) -benchmem -run=^$$ -timeout $(TIMEOUT) $(TESTARGS) ./...

checklocks: $(GOPATHBIN)/checklocks
	GOEXPERIMENT=synctest go vet -vettool=$(GOPATHBIN)/checklocks ./...

cover: vet
	rm -f cover.out && \
	GOEXPERIMENT=synctest $(GO) test -timeout $(TIMEOUT) -coverprofile cover.out ./...

coverhtml: vet
	rm -f cover.out && \
	GOEXPERIMENT=synctest $(GO) test -timeout $(TIMEOUT) -coverprofile cover.out ./... && \
	sed -i "/_easyjson/d" cover.out && \
	sed -i "/\.pb\.go/d" cover.out && \
	$(GO) tool cover -html=cover.out -o coverage.html

%_easyjson.go: %.go $(GOPATHBIN)/easyjson | $(PROTO_GO_FILES)
	rm -f easyjson-bootstrap*.go
	TMPDIR=$(TMPDIR) PATH="$(GODIR)":$(PATH) "$(GOPATHBIN)/easyjson" -all $*.go

%.pb.go: %.proto $(GOPATHBIN)/protoc-gen-go $(GOPATHBIN)/protoc-gen-go-grpc
	PATH="$(GODIR)":"$(GOPATHBIN)":$(PATH) protoc \
		--go_out=. --go_opt=paths=source_relative \
		$*.proto
	sed -i -e '1h;2,$$H;$$!d;g' -re 's|// versions.+// source:|// source:|' $*.pb.go

%_grpc.pb.go: %.proto $(GOPATHBIN)/protoc-gen-go $(GOPATHBIN)/protoc-gen-go-grpc
	PATH="$(GODIR)":"$(GOPATHBIN)":$(PATH) protoc \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$*.proto
	sed -i -e '1h;2,$$H;$$!d;g' -re 's|// versions.+// source:|// source:|' $*_grpc.pb.go

common: $(EASYJSON_GO_FILES) $(PROTO_GO_FILES) $(GRPC_PROTO_GO_FILES)
# Optimize easyjson files that could call generated functions instead of duplicating code.
	for file in $(EASYJSON_FILES); do \
		rm -f easyjson-bootstrap*.go; \
		TMPDIR=$(TMPDIR) PATH="$(GODIR)":$(PATH) "$(GOPATHBIN)/easyjson" -all $$file; \
		rm -f *_easyjson_easyjson.go; \
	done

$(BINDIR):
	mkdir -p "$(BINDIR)"

$(TMPDIR):
	mkdir -p "$(TMPDIR)"

client: $(BINDIR)/client

$(BINDIR)/client: go.mod go.sum $(CLIENT_GO_FILES) $(COMMON_GO_FILES) | $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $@ ./client/...

server: $(BINDIR)/signaling

$(BINDIR)/signaling: go.mod go.sum $(SERVER_GO_FILES) $(COMMON_GO_FILES) | $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $@ ./server/...

proxy: $(BINDIR)/proxy

$(BINDIR)/proxy: go.mod go.sum $(PROXY_GO_FILES) $(COMMON_GO_FILES) | $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $@ ./proxy/...

clean:
	rm -f easyjson-bootstrap*.go
	rm -f "$(BINDIR)/client"
	rm -f "$(BINDIR)/signaling"
	rm -f "$(BINDIR)/proxy"
	rm -rf "$(TMPDIR)"

clean-generated: clean
	rm -f $(EASYJSON_GO_FILES) $(PROTO_GO_FILES) $(GRPC_PROTO_GO_FILES)

build: server proxy

vendor: go.mod go.sum
	set -e ;\
	rm -rf $(VENDORDIR)
	$(GO) mod vendor

tarball: vendor | $(TMPDIR)
	git archive \
		--prefix=nextcloud-spreed-signaling-$(TARVERSION)/ \
		-o nextcloud-spreed-signaling-$(TARVERSION).tar \
		HEAD
	tar rf nextcloud-spreed-signaling-$(TARVERSION).tar \
		-C $(CURDIR) \
		--mtime="$(shell git log -1 --date=iso8601-strict --format=%cd HEAD)" \
		--transform "s//nextcloud-spreed-signaling-$(TARVERSION)\//" \
		vendor
	echo "$(TARVERSION)" > "$(TMPDIR)/version.txt"
	tar rf nextcloud-spreed-signaling-$(TARVERSION).tar \
		-C "$(TMPDIR)" \
		--mtime="$(shell git log -1 --date=iso8601-strict --format=%cd HEAD)" \
		--transform "s//nextcloud-spreed-signaling-$(TARVERSION)\//" \
		version.txt
	gzip --force nextcloud-spreed-signaling-$(TARVERSION).tar

dist: tarball

.NOTPARALLEL: $(EASYJSON_GO_FILES)
.PHONY: geoip/continentmap.go common vendor
.SECONDARY: $(EASYJSON_GO_FILES) $(PROTO_GO_FILES)
.DELETE_ON_ERROR:
