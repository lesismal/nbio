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
	$(GO) test $(PACKAGES)

clean:
	rm -f bin/autobahn_server
	rm -fr autobahn/report/*
	
bin/reporter:
	go build -o bin/reporter ./autobahn

autobahn: clean bin/reporter
	./autobahn/script/test.sh --build fuzzingclient config/client_tests.json examples/websocket/server_autobahn -o server.test 
	bin/reporter $(PWD)/autobahn/report/index.json

.PHONY: all vet lint test clean  autobahn
