package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	payload := strings.NewReader("hello")
	req, err := http.NewRequest("POST", "http://localhost:8888/echo", payload)
	if err != nil {
		panic(err)
	}
	req.ContentLength = -1
	req.Trailer = http.Header{
		"Trailer_key_01": []string{"Trailer_value_01"},
		"Trailer_key_02": []string{"Trailer_value_02"},
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Status)
	_, err = io.Copy(os.Stdout, resp.Body)
	if err != nil {
		panic(err)
	}
	fmt.Println("\ntrailer:", resp.Trailer)
}
