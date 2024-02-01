package nbhttp

import (
	"fmt"
	"math/rand"
	"net"
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
	defer func() {
		if parser.Conn != nil {
			parser.Conn.Close()
		}
	}()
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

	mux := &http.ServeMux{}
	mux.HandleFunc("/", func(w http.ResponseWriter, request *http.Request) {
		nRequest++
	})
	conn := newConn()
	defer conn.Close()
	processor := NewServerProcessor()
	if isClient {
		processor = NewClientProcessor(nil, func(*http.Response, error) {
			nRequest++
		})
	}
	engine := NewEngine(Config{
		Handler: mux,
	})
	parser = NewParser(conn, engine, processor, isClient, nil)
	parser.Engine = engine
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
	mux := &http.ServeMux{}
	engine := NewEngine(Config{
		Handler: mux,
	})
	conn := newConn()
	if isClient {
		processor := NewClientProcessor(nil, func(*http.Response, error) {})
		parser := NewParser(conn, engine, processor, isClient, nil)
		parser.Engine = engine
		return parser
	}
	processor := NewServerProcessor()
	parser := NewParser(conn, engine, processor, isClient, nil)
	parser.Conn = conn
	return parser
}

func newConn() net.Conn {
	var conn net.Conn
	for i := 0; i < 1000; i++ {
		addr := fmt.Sprintf("127.0.0.1:%d", 8000+i)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		go func() {
			defer ln.Close()
			ln.Accept()
		}()
		conn, err = net.Dial("tcp", addr)
		if err != nil {
			panic(err)
		}
		break
	}
	return conn
}

// func printMessage(w http.ResponseWriter, request *http.Request) {
// 	fmt.Printf("----------------------------------------------------------------\n")
// 	fmt.Println("OnRequest")
// 	fmt.Println("Method:", request.Method)
// 	fmt.Println("Path:", request.URL.Path)
// 	fmt.Println("Proto:", request.Proto)
// 	fmt.Println("Host:", request.URL.Host)
// 	fmt.Println("Rawpath:", request.URL.RawPath)
// 	fmt.Println("Content-Length:", request.ContentLength)
// 	for k, v := range request.Header {
// 		fmt.Printf("Header: [\"%v\": \"%v\"]\n", k, v)
// 	}
// 	for k, v := range request.Trailer {
// 		fmt.Printf("Trailer: [\"%v\": \"%v\"]\n", k, v)
// 	}
// 	body := request.Body
// 	if body != nil {
// 		nread := 0
// 		buffer := make([]byte, 1024)
// 		for {
// 			n, err := body.Read(buffer)
// 			if n > 0 {
// 				nread += n
// 			}
// 			if errors.Is(err, io.EOF) {
// 				break
// 			}
// 		}
// 		fmt.Println("body:", string(buffer[:nread]))
// 	} else {
// 		fmt.Println("body: null")
// 	}
// }

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
	isClient := false
	processor := NewServerProcessor()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {})
	engine := NewEngine(Config{
		Handler: mux,
	})
	parser := NewParser(newConn(), engine, processor, isClient, nil)
	defer parser.Conn.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 5; j++ {
			err := parser.Read(benchData)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
