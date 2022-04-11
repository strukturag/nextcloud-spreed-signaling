all: build

GO := $(shell which go)
GOPATH := $(shell "$(GO)" env GOPATH)
GODIR := $(shell dirname "$(GO)")
GOFMT := "$(GODIR)/gofmt"
GOOS ?= linux
GOARCH ?= amd64
GOVERSION := $(shell "$(GO)" env GOVERSION | sed "s|go||" )
BINDIR := "$(CURDIR)/bin"
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
TESTARGS := $(TESTARGS) -run $(TEST)
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
	$(GO) get -u -d github.com/mailru/easyjson/...
	$(GO) install github.com/mailru/easyjson/...

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

fmt: hook
	$(GOFMT) -s -w *.go client proxy server

vet: common
	$(GO) vet $(ALL_PACKAGES)

test: vet common
	$(GO) test -v -timeout $(TIMEOUT) $(TESTARGS) $(ALL_PACKAGES)

cover: vet common
	rm -f cover.out && \
	$(GO) test -v -timeout $(TIMEOUT) -coverprofile cover.out $(ALL_PACKAGES) && \
	sed -i "/_easyjson/d" cover.out && \
	$(GO) tool cover -func=cover.out

coverhtml: vet common
	rm -f cover.out && \
	$(GO) test -v -timeout $(TIMEOUT) -coverprofile cover.out $(ALL_PACKAGES) && \
	sed -i "/_easyjson/d" cover.out && \
	$(GO) tool cover -html=cover.out -o coverage.html

%_easyjson.go: %.go $(GOPATHBIN)/easyjson
	PATH="$(GODIR)":$(PATH) "$(GOPATHBIN)/easyjson" -all $*.go

common: \
	api_signaling_easyjson.go \
	api_backend_easyjson.go \
	api_proxy_easyjson.go \
	natsclient_easyjson.go \
	room_easyjson.go
	$(GO) mod tidy

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

build: server proxy

tarball:
	git archive \
		--prefix=nextcloud-spreed-signaling-$(TARVERSION)/ \
		-o nextcloud-spreed-signaling-$(TARVERSION).tar.gz \
		HEAD

dist: tarball

.NOTPARALLEL: %_easyjson.go
.PHONY: continentmap.go
