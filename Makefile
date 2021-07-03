all: build

GO := $(shell which go)
GOPATH := "$(CURDIR)/vendor:$(CURDIR)"
GOOS ?= linux
GOARCH ?= amd64
BINDIR := "$(CURDIR)/bin"
VERSION := $(shell "$(CURDIR)/scripts/get-version.sh")
TARVERSION := $(shell "$(CURDIR)/scripts/get-version.sh" --tar)
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
VENDORBIN := $(CURDIR)/vendor/bin
else
VENDORBIN := $(CURDIR)/vendor/bin/$(GOOS)_$(GOARCH)
endif

hook:
	[ ! -d "$(CURDIR)/.git/hooks" ] || ln -sf "$(CURDIR)/scripts/pre-commit.hook" "$(CURDIR)/.git/hooks/pre-commit"

./vendor/bin/easyjson:
	GOPATH=$(GOPATH) $(GO) get -u github.com/mailru/easyjson/...

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
	GOPATH=$(GOPATH) $(GO) get $(PACKAGE)

fmt: hook
	$(GO) fmt .

vet: common
	$(GO) vet .

test: vet common
	$(GO) test -v -timeout $(TIMEOUT) $(TESTARGS) .

cover: vet common
	rm -f cover.out && \
	GOPATH=$(GOPATH) $(GO) test -v -timeout $(TIMEOUT) -coverprofile cover.out . && \
	sed -i "/_easyjson/d" cover.out && \
	GOPATH=$(GOPATH) $(GO) tool cover -func=cover.out

coverhtml: vet common
	rm -f cover.out && \
	GOPATH=$(GOPATH) $(GO) test -v -timeout $(TIMEOUT) -coverprofile cover.out . && \
	sed -i "/_easyjson/d" cover.out && \
	GOPATH=$(GOPATH) $(GO) tool cover -html=cover.out -o coverage.html

%_easyjson.go: %.go ./vendor/bin/easyjson
	PATH=$(shell dirname $(GO)):$(PATH) GOPATH=$(GOPATH) "$(VENDORBIN)/easyjson" -all $*.go

common: \
	api_signaling_easyjson.go \
	api_backend_easyjson.go \
	api_proxy_easyjson.go \
	natsclient_easyjson.go \
	room_easyjson.go

$(BINDIR):
	mkdir -p $(BINDIR)

client: common $(BINDIR)
	GOPATH=$(GOPATH) $(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $(BINDIR)/client ./client/...

server: common $(BINDIR)
	GOPATH=$(GOPATH) $(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $(BINDIR)/signaling ./server/...

proxy: common $(BINDIR)
	GOPATH=$(GOPATH) $(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $(BINDIR)/proxy ./proxy/...

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
