package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetupRequiresTokenAndWritesSafeEnvironment(t *testing.T) {
	root := t.TempDir()
	server, err := New(Options{
		Token:   strings.Repeat("a", 64),
		Root:    root,
		DryRun:  true,
		Command: func(context.Context, string, ...string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	payload := `{"domain":"browser.example.com","gatewayMode":"direct","databaseMode":"internal","databaseHost":"","databasePort":5432,"databaseName":"cloudbrowser","databaseUser":"cloudbrowser","databasePassword":"DatabasePassword123","databaseSSLMode":"disable","adminEmail":"admin@example.com","adminPassword":"a secure administrator password","maxSessions":1}`
	request, _ := http.NewRequest("POST", httpServer.URL+"/api/setup/complete", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("setup accepted without token", response.StatusCode)
	}
	request, _ = http.NewRequest("POST", httpServer.URL+"/api/setup/complete", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatal("setup rejected valid configuration", response.StatusCode)
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
	contents, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, "DOMAIN=browser.example.com") || !strings.Contains(text, "DATABASE_URL=postgres://") {
		t.Fatal("environment is incomplete", text)
	}
	if strings.Contains(text, "a secure administrator password") {
		t.Fatal("administrator password was persisted")
	}
	var status Status
	server.mu.RLock()
	b, _ := json.Marshal(server.status)
	server.mu.RUnlock()
	_ = json.Unmarshal(b, &status)
	if status.State != "COMPLETE" || status.Origin != "https://browser.example.com" {
		t.Fatal("unexpected final status", status)
	}
}

func TestValidateRejectsUnsafeDatabasePasswordAndInvalidDomain(t *testing.T) {
	config := Config{Domain: "https://bad.example.com/path", GatewayMode: "direct", DatabaseMode: "internal", DatabaseName: "cloudbrowser", DatabaseUser: "cloudbrowser", DatabasePassword: "validPassword123", AdminEmail: "admin@example.com", AdminPassword: "a secure password", MaxSessions: 1}
	if _, err := validate(config); err == nil {
		t.Fatal("URL accepted as domain")
	}
	config.Domain = "browser.example.com"
	config.DatabasePassword = "contains$dollar"
	if _, err := validate(config); err == nil {
		t.Fatal("unsafe .env password accepted")
	}
}

func TestCompletedSetupRejectsEveryNewSubmission(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".setup-complete"), []byte("done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{Token: strings.Repeat("x", 64), Root: root, DryRun: true, Command: func(context.Context, string, ...string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	request, _ := http.NewRequest("POST", httpServer.URL+"/api/setup/complete", strings.NewReader("{}"))
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
