package main

import (
	"cloudbrowser/internal/setup"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	if os.Getenv("SETUP_DRY_RUN") != "1" {
		log.Fatal("standalone production setup moved to the cloud-browser command; set SETUP_DRY_RUN=1 for UI preview")
	}
	server, err := setup.New(setup.Options{
		Token:  os.Getenv("SETUP_TOKEN"),
		Root:   os.Getenv("SETUP_ROOT"),
		DryRun: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	httpServer := &http.Server{
		Addr:              env("SETUP_LISTEN", ":8090"),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("Cloud Browser setup listening on %s", httpServer.Addr)
	log.Fatal(httpServer.ListenAndServe())
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
