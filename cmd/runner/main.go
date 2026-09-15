package main

import (
	"cloudbrowser/internal/protocol"
	"cloudbrowser/internal/runner"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	r := runner.New()
	if e := os.MkdirAll(r.Root, 0700); e != nil {
		log.Fatal(e)
	}
	path := protocol.Env("RUNNER_SOCKET", "/run/cloud-browser/runner.sock")
	if e := os.MkdirAll(filepath.Dir(path), 0750); e != nil {
		log.Fatal(e)
	}
	if e := os.Chown(filepath.Dir(path), 0, 10001); e != nil {
		log.Fatal(e)
	}
	if e := os.Chmod(filepath.Dir(path), 0750); e != nil {
		log.Fatal(e)
	}
	if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
		log.Fatal(e)
	}
	l, e := net.Listen("unix", path)
	if e != nil {
		log.Fatal(e)
	}
	defer l.Close()
	if e = os.Chmod(path, 0660); e != nil {
		log.Fatal(e)
	}
	if e = os.Chown(path, 0, 10001); e != nil {
		log.Fatal(e)
	}
	log.Print("Runner listening on Unix socket")
	srv := &http.Server{Handler: r.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(srv.Serve(l))
}
