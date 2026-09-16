package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const internalPayload = `{"domain":"browser.example.com","gatewayMode":"direct","databaseMode":"internal","databaseHost":"","databasePort":0,"databaseName":"","databaseUser":"","databasePassword":"","databaseSSLMode":"","adminEmail":"admin@example.com","adminPassword":"a secure administrator password","maxSessions":1}`

func TestInternalSetupNeedsNoDatabaseCredential(t *testing.T) {
	root := t.TempDir()
	server, err := New(Options{Token: strings.Repeat("a", 64), Root: root, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	request, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/setup/complete", strings.NewReader(internalPayload))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("setup accepted without token", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodPost, httpServer.URL+"/api/setup/complete", strings.NewReader(internalPayload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatal("setup rejected valid internal configuration", response.StatusCode)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.mu.RLock()
		state := server.status.State
		server.mu.RUnlock()
		if state == "COMPLETE" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.mu.RLock()
	data, _ := json.Marshal(server.status)
	server.mu.RUnlock()
	var status Status
	_ = json.Unmarshal(data, &status)
	if status.State != "COMPLETE" || status.Origin != "https://browser.example.com" {
		t.Fatal("unexpected final status", status)
	}
	if _, err = os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
		t.Fatal("dry-run setup unexpectedly wrote deployment configuration")
	}
}

func TestOpeningWizardDoesNotStartInstallationOrDatabase(t *testing.T) {
	var installs atomic.Int32
	server, err := New(Options{
		Token: strings.Repeat("c", 64), Root: t.TempDir(),
		Install: func(context.Context, Config, UpdateFunc) error {
			installs.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	for _, path := range []string{"/", "/api/setup/status", "/api/setup/environment"} {
		request, _ := http.NewRequest(http.MethodGet, httpServer.URL+path, nil)
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response.Body.Close()
	}
	if installs.Load() != 0 {
		t.Fatal("opening or inspecting the wizard started installation")
	}
}

func TestExistingDatabaseTestUsesNormalizedContainerAddress(t *testing.T) {
	var calls atomic.Int32
	var received Config
	server, err := New(Options{
		Token: strings.Repeat("b", 64), Root: t.TempDir(), DryRun: true,
		TestDatabase: func(_ context.Context, config Config) error {
			calls.Add(1)
			received = config
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	payload := `{"databaseMode":"local","databaseHost":"127.0.0.1","databasePort":5432,"databaseName":"cloudbrowser","databaseUser":"cloudbrowser","databasePassword":"secret","databaseSSLMode":"disable"}`
	request, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/setup/database/test", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || calls.Load() != 1 {
		t.Fatal("database test did not run", response.StatusCode, calls.Load())
	}
	if received.DatabaseHost != "host.docker.internal" {
		t.Fatal("local database did not use Docker host gateway", received.DatabaseHost)
	}
}

func TestValidateDatabaseModesAndDomain(t *testing.T) {
	config := Config{Domain: "https://bad.example.com/path", GatewayMode: "direct", DatabaseMode: "internal", AdminEmail: "admin@example.com", AdminPassword: "a secure password", MaxSessions: 1}
	if _, err := validate(config); err == nil {
		t.Fatal("URL accepted as domain")
	}
	config.Domain = "browser.example.com"
	config.DatabaseMode = "remote"
	config.DatabaseHost = "db.example.com"
	config.DatabasePort = 5432
	config.DatabaseName = "cloudbrowser"
	config.DatabaseUser = "cloudbrowser"
	config.DatabasePassword = "p@ss:$ with symbols"
	config.DatabaseSSLMode = "require"
	if _, err := validate(config); err != nil {
		t.Fatal("valid remote database rejected", err)
	}
	config.DatabasePassword = ""
	if _, err := validate(config); err == nil {
		t.Fatal("empty existing database password accepted")
	}
}

func TestCompletedSetupRejectsEveryNewSubmission(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".setup-complete"), []byte("done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{Token: strings.Repeat("x", 64), Root: root, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	request, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/setup/complete", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 64))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatal("completed setup accepted a request", response.StatusCode)
	}
}
