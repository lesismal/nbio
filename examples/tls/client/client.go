package main

import (
	"crypto/tls"
	"io"
	"log"
)

func main() {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}

	conn, err := tls.Dial("tcp", "localhost:8888", tlsConfig)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	wbuf := []byte("hello")
	n1, err := conn.Write(wbuf)
	if err != nil || n1 != len(wbuf) {
		log.Fatalf("conn.Write failed: %v, %v", n1, err)
	}

	rbuf := make([]byte, len(wbuf))
	n2, err := io.ReadFull(conn, rbuf)
	if err != nil {
		log.Fatalf("conn.Read failed: %v", err)
	}
	if n2 != n1 || string(rbuf) != string(wbuf) {
		log.Fatalf("conn.Read failed: %v, %v", n2, string(wbuf))
	}
	log.Println("response:", string(rbuf))
}
