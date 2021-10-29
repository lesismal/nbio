package nbhttp

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"testing"
	"time"
)

func TestServerParserContentLength(t *testing.T) {
	data := []byte("POST /echo HTTP/1.1\r\nEmpty:\r\n Empty2:\r\nHost : localhost:8080   \r\nConnection: close \r\n Accept-Encoding :  gzip , deflate ,br  \r\n\r\n")
	err := testParser(t, false, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}

	data = []byte("POST /echo HTTP/1.1\r\nHost: localhost:8080\r\n Connection: close \r\nContent-Length :  0\r\nAccept-Encoding : gzip \r\n\r\n")
	err = testParser(t, false, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}

	data = []byte("POST /echo HTTP/1.1\r\nHost: localhost:8080\r\n Connection: close \r\nContent-Length :  5\r\nAccept-Encoding : gzip \r\n\r\nhello")
	err = testParser(t, false, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
}

func TestServerParserChunks(t *testing.T) {
	data := []byte("POST / HTTP/1.1\r\nHost: localhost:1235\r\n User-Agent: Go-http-client/1.1\r\nTransfer-Encoding: chunked\r\nAccept-Encoding: gzip\r\n\r\n4   \r\nbody\r\n0  \r\n\r\n")
	err := testParser(t, false, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}

	data = []byte("POST / HTTP/1.1\r\nHost: localhost:1235\r\n User-Agent: Go-http-client/1.1\r\nTransfer-Encoding: chunked\r\nAccept-Encoding: gzip\r\n\r\n0\r\n\r\n")
	err = testParser(t, false, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
}

func TestServerParserTrailer(t *testing.T) {
	data := []byte("POST / HTTP/1.1\r\nHost : localhost:1235\r\n User-Agent  : Go-http-client/1.1   \r\nTransfer-Encoding: chunked\r\nTrailer: Md5,Size\r\nAccept-Encoding: gzip  \r\n\r\n4\r\nbody\r\n0\r\n  Md5  : 841a2d689ad86bd1611447453c22c6fc \r\n Size  : 4  \r\n\r\n")
	err := testParser(t, false, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
	data = []byte("POST / HTTP/1.1\r\nHost: localhost:1235\r\nUser-Agent: Go-http-client/1.1\r\nTransfer-Encoding: chunked\r\nTrailer: Md5,Size\r\nAccept-Encoding: gzip  \r\n\r\n0\r\nMd5 : 841a2d689ad86bd1611447453c22c6fc \r\n Size: 4 \r\n\r\n")
	err = testParser(t, false, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
}

func TestClientParserContentLength(t *testing.T) {
	data := []byte("HTTP/1.1 200 OK\r\nHost: localhost:8080\r\n Connection: close \r\n Accept-Encoding : gzip  \r\n\r\n")
	err := testParser(t, true, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}

	data = []byte("HTTP/1.1 200 OK\r\nHost: localhost:8080\r\n Connection: close \r\n Content-Length :  0\r\nAccept-Encoding : gzip \r\n\r\n")
	err = testParser(t, true, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}

	data = []byte("HTTP/1.1 200 OK\r\nHost: localhost:8080\r\n Connection: close \r\n Content-Length :  5\r\nAccept-Encoding : gzip \r\n\r\nhello")
	err = testParser(t, true, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
}

func TestClientParserChunks(t *testing.T) {
	data := []byte("HTTP/1.1 200 OK\r\nHost: localhost:1235\r\n User-Agent: Go-http-client/1.1\r\n Transfer-Encoding: chunked\r\nAccept-Encoding: gzip\r\n\r\n4\r\nbody\r\n0\r\n\r\n")
	err := testParser(t, true, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
	data = []byte("HTTP/1.1 200 OK\r\nHost: localhost:1235\r\nUser-Agent: Go-http-client/1.1\r\nTransfer-Encoding: chunked\r\nAccept-Encoding: gzip\r\n\r\n0\r\n\r\n")
	err = testParser(t, true, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
}

func TestClientParserTrailer(t *testing.T) {
	data := []byte("HTTP/1.1 200 OK\r\nHost: localhost:1235\r\n User-Agent: Go-http-client/1.1\r\n Transfer-Encoding: chunked\r\nTrailer: Md5,Size\r\nAccept-Encoding: gzip\r\n\r\n4\r\nbody\r\n0\r\nMd5: 841a2d689ad86bd1611447453c22c6fc\r\nSize: 4\r\n\r\n")
	err := testParser(t, true, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
	data = []byte("HTTP/1.1 200 OK\r\nHost: localhost:1235\r\nUser-Agent: Go-http-client/1.1\r\nTransfer-Encoding : chunked\r\nTrailer: Md5,Size\r\nAccept-Encoding: gzip\r\n\r\n0\r\nMd5: 841a2d689ad86bd1611447453c22c6fc\r\nSize: 4\r\n\r\n")
	err = testParser(t, true, data)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
}

func testParser(t *testing.T, isClient bool, data []byte) error {
	parser := newParser(isClient)
	err := parser.Read(data)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < len(data)-1; i++ {
		err = parser.Read(append([]byte{}, data[i:i+1]...))
		if err != nil {
			t.Fatal(err)
		}
	}
	err = parser.Read(append([]byte{}, data[len(data)-1:]...))
	if err != nil {
		t.Fatal(err)
	}

	nRequest := 0
	data = append(data, data...)

	maxReadSize := 1024 * 1024 * 4
	mux := &http.ServeMux{}
	mux.HandleFunc("/", func(w http.ResponseWriter, request *http.Request) {
		nRequest++
	})
	processor := NewServerProcessor(nil, mux, DefaultKeepaliveTime, false)
	if isClient {
		processor = NewClientProcessor(nil, func(*http.Response, error) {
			nRequest++
		})
	}
	svr := NewServer(Config{})
	parser = NewParser(processor, isClient, maxReadSize, nil)
	parser.Engine = svr.Engine
	if pr, ok := processor.(*ServerProcessor); ok {
		pr.parser = parser
	} else {
		processor.(*ClientProcessor).conn = &ClientConn{Engine: parser.Engine}
	}
	tBegin := time.Now()
	loop := 10000
	for i := 0; i < loop; i++ {
		tmp := data
		reads := [][]byte{}
		for len(tmp) > 0 {
			nRead := rand.Intn(len(tmp)) + 1
			readBuf := append([]byte{}, tmp[:nRead]...)
			reads = append(reads, readBuf)
			tmp = tmp[nRead:]
			err = parser.Read(readBuf)
			if err != nil {
				t.Fatalf("nRead: %v, numOne: %v, reads: %v, error: %v", len(data)-len(tmp), len(data), reads, err)
			}

		}
		if nRequest != (i+1)*2 {
			return fmt.Errorf("nRequest: %v, %v", i, nRequest)
		}
	}
	tUsed := time.Since(tBegin)
	fmt.Printf("%v loops, %v s used, %v ns/op, %v req/s\n", loop, tUsed.Seconds(), tUsed.Nanoseconds()/int64(loop), float64(loop)/tUsed.Seconds())

	return nil
}

func newParser(isClient bool) *Parser {
	engine := NewEngine(Config{})
	maxReadSize := 1024 * 1024 * 4
	if isClient {
		processor := NewClientProcessor(nil, func(*http.Response, error) {})
		parser := NewParser(processor, isClient, maxReadSize, nil)
		parser.Engine = engine
		processor.(*ClientProcessor).conn = &ClientConn{Engine: engine}
		return parser
	}
	mux := &http.ServeMux{}
	mux.HandleFunc("/", pirntMessage)
	processor := NewServerProcessor(nil, mux, DefaultKeepaliveTime, false)

	parser := NewParser(processor, isClient, maxReadSize, nil)
	parser.Engine = engine
	processor.(*ServerProcessor).parser = parser
	return parser
}

func pirntMessage(w http.ResponseWriter, request *http.Request) {
	fmt.Printf("----------------------------------------------------------------\n")
	fmt.Println("OnRequest")
	fmt.Println("Method:", request.Method)
	fmt.Println("Path:", request.URL.Path)
	fmt.Println("Proto:", request.Proto)
	fmt.Println("Host:", request.URL.Host)
	fmt.Println("Rawpath:", request.URL.RawPath)
	fmt.Println("Content-Length:", request.ContentLength)
	for k, v := range request.Header {
		fmt.Printf("Header: [\"%v\": \"%v\"]\n", k, v)
	}
	for k, v := range request.Trailer {
		fmt.Printf("Trailer: [\"%v\": \"%v\"]\n", k, v)
	}
	body := request.Body
	if body != nil {
		nread := 0
		buffer := make([]byte, 1024)
		for {
			n, err := body.Read(buffer)
			if n > 0 {
				nread += n
			}
			if errors.Is(err, io.EOF) {
				break
			}
		}
		fmt.Println("body:", string(buffer[:nread]))
	} else {
		fmt.Println("body: null")
	}
}

var benchData = []byte("POST /joyent/http-parser HTTP/1.1\r\n" +
	"Host: github.com\r\n" +
	"DNT: 1\r\n" +
	"Accept-Encoding: gzip, deflate, sdch\r\n" +
	"Accept-Language: ru-RU,ru;q=0.8,en-US;q=0.6,en;q=0.4\r\n" +
	"User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_10_1) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) " +
	"Chrome/39.0.2171.65 Safari/537.36\r\n" +
	"Accept: text/html,application/xhtml+xml,application/xml;q=0.9," +
	"image/webp,*/*;q=0.8\r\n" +
	"Referer: https://github.com/joyent/http-parser\r\n" +
	"Connection: keep-alive\r\n" +
	"Transfer-Encoding: chunked\r\n" +
	"Cache-Control: max-age=0\r\n\r\nb\r\nhello world\r\n0\r\n\r\n")

func BenchmarkServerProcessor(b *testing.B) {
	maxReadSize := 1024 * 1024 * 4
	isClient := false
	mux := &http.ServeMux{}
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {})
	processor := NewServerProcessor(nil, mux, DefaultKeepaliveTime, false)
	parser := NewParser(processor, isClient, maxReadSize, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := parser.Read(benchData); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEmpryProcessor(b *testing.B) {
	maxReadSize := 1024 * 1024 * 4
	isClient := false
	// processor := NewEmptyProcessor()
	parser := NewParser(nil, isClient, maxReadSize, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := parser.Read(benchData); err != nil {
			b.Fatal(err)
		}
	}
}
