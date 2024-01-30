all: build

GO := $(shell which go)
GOPATH := $(shell "$(GO)" env GOPATH)
GODIR := $(shell dirname "$(GO)")
GOFMT := "$(GODIR)/gofmt"
GOOS ?= linux
GOARCH ?= amd64
GOVERSION := $(shell "$(GO)" env GOVERSION | sed "s|go||" )
BINDIR := "$(CURDIR)/bin"
VENDORDIR := "$(CURDIR)/vendor"
VERSION := $(shell "$(CURDIR)/scripts/get-version.sh")
TARVERSION := $(shell "$(CURDIR)/scripts/get-version.sh" --tar)
PACKAGENAME := github.com/strukturag/nextcloud-spreed-signaling
ALL_PACKAGES := $(PACKAGENAME) $(PACKAGENAME)/client $(PACKAGENAME)/proxy $(PACKAGENAME)/server

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

ifeq ($(GOARCH), amd64)
GOPATHBIN := $(GOPATH)/bin
else
GOPATHBIN := $(GOPATH)/bin/$(GOOS)_$(GOARCH)
endif

hook:
	[ ! -d "$(CURDIR)/.git/hooks" ] || ln -sf "$(CURDIR)/scripts/pre-commit.hook" "$(CURDIR)/.git/hooks/pre-commit"

$(GOPATHBIN)/easyjson:
	[ "$(GOPROXY)" = "off" ] || $(GO) get -u -d github.com/mailru/easyjson/...
	$(GO) install github.com/mailru/easyjson/...

$(GOPATHBIN)/protoc-gen-go:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go

$(GOPATHBIN)/protoc-gen-go-grpc:
	[ "$(GOPROXY)" = "off" ] || $(GO) get -u -d google.golang.org/grpc/cmd/protoc-gen-go-grpc
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

fmt: hook | common_proto
	$(GOFMT) -s -w *.go client proxy server

vet: common
	$(GO) vet $(ALL_PACKAGES)

test: vet common
	$(GO) test -v -timeout $(TIMEOUT) $(TESTARGS) $(ALL_PACKAGES)

cover: vet common
	rm -f cover.out && \
	$(GO) test -v -timeout $(TIMEOUT) -coverprofile cover.out $(ALL_PACKAGES) && \
	sed -i "/_easyjson/d" cover.out && \
	sed -i "/\.pb\.go/d" cover.out && \
	$(GO) tool cover -func=cover.out

coverhtml: vet common
	rm -f cover.out && \
	$(GO) test -v -timeout $(TIMEOUT) -coverprofile cover.out $(ALL_PACKAGES) && \
	sed -i "/_easyjson/d" cover.out && \
	sed -i "/\.pb\.go/d" cover.out && \
	$(GO) tool cover -html=cover.out -o coverage.html

%_easyjson.go: %.go $(GOPATHBIN)/easyjson | common_proto
	PATH="$(GODIR)":$(PATH) "$(GOPATHBIN)/easyjson" -all $*.go

%.pb.go: %.proto $(GOPATHBIN)/protoc-gen-go $(GOPATHBIN)/protoc-gen-go-grpc
	PATH="$(GODIR)":"$(GOPATHBIN)":$(PATH) protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$*.proto

common: common_easyjson common_proto

common_easyjson: \
	api_async_easyjson.go \
	api_backend_easyjson.go \
	api_grpc_easyjson.go \
	api_proxy_easyjson.go \
	api_signaling_easyjson.go

common_proto: \
	grpc_backend.pb.go \
	grpc_internal.pb.go \
	grpc_mcu.pb.go \
	grpc_sessions.pb.go

$(BINDIR):
	mkdir -p $(BINDIR)

client: common $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $(BINDIR)/client ./client/...

server: common $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $(BINDIR)/signaling ./server/...

proxy: common $(BINDIR)
	$(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $(BINDIR)/proxy ./proxy/...

clean:
	rm -f *_easyjson.go
	rm -f easyjson-bootstrap*.go
	rm -f *.pb.go

build: server proxy

vendor: go.mod go.sum common
	set -e ;\
	rm -rf $(VENDORDIR)
	$(GO) mod tidy; \
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

.NOTPARALLEL: %_easyjson.go
.PHONY: continentmap.go vendor
