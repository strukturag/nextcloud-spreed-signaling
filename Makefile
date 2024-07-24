all: build

GO := $(shell which go)
GOPATH := $(shell "$(GO)" env GOPATH)
GODIR := $(shell dirname "$(GO)")
GOFMT := "$(GODIR)/gofmt"
GOOS ?= linux
GOARCH ?= amd64
GOVERSION := $(shell "$(GO)" env GOVERSION | sed "s|go||" )
BINDIR := $(CURDIR)/bin
VENDORDIR := "$(CURDIR)/vendor"
VERSION := $(shell "$(CURDIR)/scripts/get-version.sh")
TARVERSION := $(shell "$(CURDIR)/scripts/get-version.sh" --tar)
PACKAGENAME := github.com/strukturag/nextcloud-spreed-signaling
ALL_PACKAGES := $(PACKAGENAME) $(PACKAGENAME)/client $(PACKAGENAME)/proxy $(PACKAGENAME)/server
PROTO_FILES := $(basename $(wildcard *.proto))
PROTO_GO_FILES := $(addsuffix .pb.go,$(PROTO_FILES)) $(addsuffix _grpc.pb.go,$(PROTO_FILES))
EASYJSON_GO_FILES := \
	api_async_easyjson.go \
	api_backend_easyjson.go \
	api_grpc_easyjson.go \
	api_proxy_easyjson.go \
	api_signaling_easyjson.go

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

$(GOPATHBIN)/easyjson: go.mod go.sum
	$(GO) install github.com/mailru/easyjson/...

$(GOPATHBIN)/protoc-gen-go: go.mod go.sum
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go

$(GOPATHBIN)/protoc-gen-go-grpc: go.mod go.sum
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc

continentmap.go:
	$(CURDIR)/scripts/get_continent_map.py $@

check-continentmap:
	set -e ;\
	TMP=$$(mktemp -d) ;\
	echo Make sure to remove $$TMP on error ;\
	$(CURDIR)/scripts/get_continent_map.py $$TMP/continentmap.go ;\
	diff -u continentmap.go $$TMP/continentmap.go ;\
	rm -rf $$TMP

get:
	$(GO) get $(PACKAGE)

fmt: hook | $(PROTO_GO_FILES)
	$(GOFMT) -s -w *.go client proxy server

vet:
	$(GO) vet $(ALL_PACKAGES)

test: vet
	$(GO) test -timeout $(TIMEOUT) $(TESTARGS) $(ALL_PACKAGES)

cover: vet
	rm -f cover.out && \
	$(GO) test -timeout $(TIMEOUT) -coverprofile cover.out $(ALL_PACKAGES) && \
	sed -i "/_easyjson/d" cover.out && \
	sed -i "/\.pb\.go/d" cover.out && \
	$(GO) tool cover -func=cover.out

coverhtml: vet
	rm -f cover.out && \
	$(GO) test -timeout $(TIMEOUT) -coverprofile cover.out $(ALL_PACKAGES) && \
	sed -i "/_easyjson/d" cover.out && \
	sed -i "/\.pb\.go/d" cover.out && \
	$(GO) tool cover -html=cover.out -o coverage.html

%_easyjson.go: %.go $(GOPATHBIN)/easyjson | $(PROTO_GO_FILES)
	rm -f easyjson-bootstrap*.go
	PATH="$(GODIR)":$(PATH) "$(GOPATHBIN)/easyjson" -all $*.go

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

common: $(EASYJSON_GO_FILES) $(PROTO_GO_FILES)

$(BINDIR):
	mkdir -p "$(BINDIR)"

client: $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o "$(BINDIR)/client" ./client/...

server: $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o "$(BINDIR)/signaling" ./server/...

proxy: $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o "$(BINDIR)/proxy" ./proxy/...

clean:
	rm -f easyjson-bootstrap*.go

build: server proxy

vendor: go.mod go.sum
	set -e ;\
	rm -rf $(VENDORDIR)
	$(GO) mod vendor

tarball: vendor
	git archive \
		--prefix=nextcloud-spreed-signaling-$(TARVERSION)/ \
		-o nextcloud-spreed-signaling-$(TARVERSION).tar \
		HEAD
	tar rf nextcloud-spreed-signaling-$(TARVERSION).tar \
		-C $(CURDIR) \
		--mtime="$(shell git log -1 --date=iso8601-strict --format=%cd HEAD)" \
		--transform "s//nextcloud-spreed-signaling-$(TARVERSION)\//" \
		vendor
	gzip --force nextcloud-spreed-signaling-$(TARVERSION).tar

dist: tarball

.NOTPARALLEL: $(EASYJSON_GO_FILES)
.PHONY: continentmap.go common vendor
.SECONDARY: $(EASYJSON_GO_FILES) $(PROTO_GO_FILES)
.DELETE_ON_ERROR:
