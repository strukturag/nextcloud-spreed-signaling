all: build

GO := $(shell which go)
GOPATH := "$(CURDIR)/vendor:$(CURDIR)"
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

hook:
	[ ! -d "$(CURDIR)/.git/hooks" ] || ln -sf "$(CURDIR)/scripts/pre-commit.hook" "$(CURDIR)/.git/hooks/pre-commit"

godeps:
	GOPATH=$(GOPATH) $(GO) get github.com/rogpeppe/godeps

easyjson: dependencies
	GOPATH=$(GOPATH) $(GO) get -d github.com/mailru/easyjson/...
	GOPATH=$(GOPATH) $(GO) build -o ./vendor/bin/easyjson ./vendor/src/github.com/mailru/easyjson/easyjson/main.go

dependencies: hook godeps
	GOPATH=$(GOPATH) ./vendor/bin/godeps -u dependencies.tsv

dependencies.tsv: godeps
	set -e ;\
	TMP=$$(mktemp -d) ;\
	echo Make sure to remove $$TMP on error ;\
	cp -r "$(CURDIR)/vendor" $$TMP ;\
	GOPATH=$$TMP/vendor:"$(CURDIR)" "$(CURDIR)/vendor/bin/godeps" ./src/... > "$(CURDIR)/dependencies.tsv" ;\
	rm -rf $$TMP

src/signaling/continentmap.go:
	$(CURDIR)/scripts/get_continent_map.py $@

check-continentmap:
	set -e ;\
	TMP=$$(mktemp -d) ;\
	echo Make sure to remove $$TMP on error ;\
	$(CURDIR)/scripts/get_continent_map.py $$TMP/continentmap.go ;\
	diff -u src/signaling/continentmap.go $$TMP/continentmap.go ;\
	rm -rf $$TMP

get:
	GOPATH=$(GOPATH) $(GO) get $(PACKAGE)

fmt: hook
	$(GO) fmt ./src/...

vet: dependencies common
	GOPATH=$(GOPATH) $(GO) vet ./src/...

test: dependencies vet common
	GOPATH=$(GOPATH) $(GO) test -v -timeout $(TIMEOUT) $(TESTARGS) ./src/...

cover: dependencies vet common
	rm -f cover.out && \
	GOPATH=$(GOPATH) $(GO) test -v -timeout $(TIMEOUT) -coverprofile cover.out ./src/signaling/... && \
	sed -i "/_easyjson/d" cover.out && \
	GOPATH=$(GOPATH) $(GO) tool cover -func=cover.out

coverhtml: dependencies vet common
	rm -f cover.out && \
	GOPATH=$(GOPATH) $(GO) test -v -timeout $(TIMEOUT) -coverprofile cover.out ./src/signaling/... && \
	sed -i "/_easyjson/d" cover.out && \
	GOPATH=$(GOPATH) $(GO) tool cover -html=cover.out -o coverage.html

%_easyjson.go: %.go
	PATH=$(shell dirname $(GO)):$(PATH) GOPATH=$(GOPATH) ./vendor/bin/easyjson -all $*.go

common: easyjson \
	src/signaling/api_signaling_easyjson.go \
	src/signaling/api_backend_easyjson.go \
	src/signaling/api_proxy_easyjson.go \
	src/signaling/natsclient_easyjson.go \
	src/signaling/room_easyjson.go

client: dependencies common
	mkdir -p $(BINDIR)
	GOPATH=$(GOPATH) $(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $(BINDIR)/client ./src/client/...

server: dependencies common
	mkdir -p $(BINDIR)
	GOPATH=$(GOPATH) $(GO) build $(BUILDARGS) -ldflags '$(INTERNALLDFLAGS)' -o $(BINDIR)/signaling ./src/server/...

clean:
	rm -f src/signaling/*_easyjson.go

build: server

tarball:
	git archive \
		--prefix=nextcloud-spreed-signaling-$(TARVERSION)/ \
		-o nextcloud-spreed-signaling-$(TARVERSION).tar.gz \
		HEAD

dist: tarball

.PHONY: src/signaling/continentmap.go
