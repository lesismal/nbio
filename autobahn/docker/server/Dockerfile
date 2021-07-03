FROM golang:1.16-alpine3.13

ARG testfile=tfile
WORKDIR /go/src/github.com/lesismal/nbio

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .
ENV CGO_ENABLED=0

RUN go test -c -tags autobahn -coverpkg "github.com/lesismal/nbio/..."  "github.com/lesismal/nbio/${testfile}" -o "server.test"

ENTRYPOINT ["./server.test", "-test.coverprofile", "/report/server.coverage"]