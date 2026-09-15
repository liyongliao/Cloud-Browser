package main

import (
	"cloudbrowser/internal/agent"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	a := &agent.Agent{Token: os.Getenv("AGENT_TOKEN"), Home: os.Getenv("HOME")}
	if a.Token == "" {
		log.Fatal("AGENT_TOKEN required")
	}
	srv := &http.Server{Addr: ":8081", Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	go func() {
		<-ctx.Done()
		c, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		agent.CloseBrowser(c)
		_ = srv.Shutdown(c)
	}()
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
