package main

import (
	"cloudbrowser/internal/api"
	"cloudbrowser/internal/protocol"
	"cloudbrowser/migrations"
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	db, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(ctx, migrations.SQL); e != nil {
		log.Fatal(e)
	}
	a := api.New(db, protocol.Env("PUBLIC_ORIGIN", "http://localhost:8080"), protocol.Env("RUNNER_SOCKET", "/run/cloud-browser/runner.sock"))
	defer a.Close()
	go a.Work(ctx)
	srv := &http.Server{Addr: protocol.Env("LISTEN", ":8080"), Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		a.Close()
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = srv.Shutdown(c)
	}()
	log.Print("Cloud Browser API listening")
	if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
