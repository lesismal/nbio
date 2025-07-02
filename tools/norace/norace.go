package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	root, _ = filepath.Abs("./")

	skipPaths = []string{
		"_test.go",
		"norace.go",
	}

	chTask = make(chan func(), 32)
)

func main() {
	defer close(chTask)

	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			for f := range chTask {
				f()
			}
		}()
	}

	wg := &sync.WaitGroup{}

	walk(wg, root)

	// wg.Done()
	fmt.Println("wait")
	wg.Wait()

	fmt.Println("exit")
}

func run(f func()) {
	chTask <- f
}

func walk(wg *sync.WaitGroup, currRoot string) {
	err := filepath.Walk(currRoot, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {

		} else {
			if shouldSkip(path) {
				return nil
			}
			wg.Add(1)
			run(func() {
				defer wg.Done()
				addNorace(path, info)
			})
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
}

func shouldSkip(path string) bool {
	path, _ = filepath.Abs(path)
	if !strings.HasSuffix(path, ".go") {
		return true
	}
	for _, v := range skipPaths {
		if strings.Contains(path, v) {
			return true
		}
	}
	return path == root || path == "." || path == "./" || path == "\\."
}

func addNorace(path string, info os.FileInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	s := string(data)
	s = strings.ReplaceAll(s, "\nfunc", "\n//go:norace\nfunc")
	tag := "//go:norace\n"
	tag2 := tag + tag
	for strings.Contains(s, tag2) {
		s = strings.ReplaceAll(s, tag2, tag)
	}
	data = []byte(s)

	tmpFile := path + time.Now().Format(".20060102150405.dec")
	err = os.WriteFile(tmpFile, data, info.Mode().Perm())
	if err != nil {
		log.Printf("xxx WriteFile origin [%v] failed: %v", path, err)
		panic(err)
	}

	err = os.Remove(path)
	if err != nil {
		log.Printf("xxx Remove origin [%v] failed: %v", path, err)
		panic(err)
	}

	err = os.Rename(tmpFile, path)
	if err != nil {
		log.Printf("xxx Rename tmp file[%v] -> origin file[%v] failed: %v", tmpFile, path, err)
		panic(err)
	}
	log.Printf("+++ add norace for file[%v]", path)
}
