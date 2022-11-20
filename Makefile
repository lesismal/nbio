GO?=go
PACKAGE_DIRS=       $(shell $(GO) list -f '{{ .Dir }}' ./...|grep -v 'lesismal/nbio/examples')
PACKAGES=           $(shell $(GO) list ./...|grep -v 'lesismal/nbio/examples')
.PHONY: all vet lint

all: vet lint test

vet:
	$(GO) vet $(PACKAGES)

lint:
	golangci-lint run $(PACKAGE_DIRS)

test:
	$(GO) test -v $(PACKAGES)

clean:
	rm -rf ./autobahn/bin/*
	rm -rf ./autobahn/report/*

autobahn/init:
	mkdir -p ./autobahn/bin

autobahn/nbio_server:
	$(GO) build -o ./autobahn/bin/nbio_autobahn_server ./autobahn/server

autobahn/nbio_reporter:
	$(GO) build -o ./autobahn/bin/nbio_autobahn_reporter ./autobahn/reporter

autobahn: clean autobahn/init autobahn/nbio_server autobahn/nbio_reporter
	./autobahn/script/run_autobahn.sh

.PHONY: all vet lint test clean  autobahn

