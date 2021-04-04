package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

func get() {
	url := "http://localhost:8888/echo"
	method := "GET"
	client := &http.Client{}
	for i := 0; i < 5; i++ {
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			panic(err)
		}
		res, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()
		body, err := ioutil.ReadAll(res.Body)
		fmt.Println("get:", string(body))
	}
}

func post() {
	url := "http://localhost:8888/echo"
	method := "POST"
	client := &http.Client{}

	for i := 0; i < 5; i++ {
		payload := strings.NewReader("hello")
		req, err := http.NewRequest(method, url, payload)
		if err != nil {
			panic(err)
		}
		res, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()
		body, err := ioutil.ReadAll(res.Body)
		fmt.Println("post:", string(body))
	}
}

func main() {
	get()
	post()
}
