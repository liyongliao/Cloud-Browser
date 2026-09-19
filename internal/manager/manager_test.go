package manager

import (
	"cloudbrowser/internal/setup"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingDatabaseEnvironmentDoesNotEnablePostgres(t *testing.T) {
	root := t.TempDir()
	installer := NewInstaller(root)
	config := setup.Config{
		Domain: "browser.example.com", DatabaseMode: "remote",
		DatabaseHost: "db.example.com", DatabasePort: 5432,
		DatabaseName: "cloudbrowser", DatabaseUser: "cloudbrowser",
		DatabasePassword: "p@ss word", DatabaseSSLMode: "require", MaxSessions: 1,
	}
	if err := installer.writeEnvironment(config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "POSTGRES_PASSWORD=") || strings.Contains(text, "COMPOSE_PROFILES") {
		t.Fatal("existing database configuration enabled an internal PostgreSQL", text)
	}
	if !strings.Contains(text, "DATABASE_MODE=remote") || !strings.Contains(text, "p%40ss%20word") {
		t.Fatal("database URL was not safely encoded", text)
	}
}

func TestInternalDatabaseUsesOverlayOnlyOnce(t *testing.T) {
	installation := Installation{Kind: "managed", Root: "/var/lib/cloud-browser", DatabaseMode: "internal"}
	args := composeArgs(installation, "up", "-d")
	joined := strings.Join(args, " ")
	if strings.Count(joined, "compose.database.yaml") != 1 {
		t.Fatal("database overlay missing or duplicated", joined)
	}
	installation.DatabaseMode = "remote"
	if strings.Contains(strings.Join(composeArgs(installation, "up", "-d"), " "), "compose.database.yaml") {
		t.Fatal("remote database unexpectedly loaded the internal database overlay")
	}
}

func TestInternalDatabasePasswordIsReusedAcrossRetry(t *testing.T) {
	installer := NewInstaller(t.TempDir())
	first, err := installer.internalDatabasePassword()
	if err != nil {
		t.Fatal(err)
	}
	second, err := installer.internalDatabasePassword()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatal("internal database retry generated a second credential")
	}
}

func TestEmbeddedRuntimeComposeHasNoPostgresService(t *testing.T) {
	data, err := managerAssets.ReadFile("assets/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "postgres:") || strings.Contains(string(data), "POSTGRES_PASSWORD") {
		t.Fatal("base runtime compose declares PostgreSQL")
	}
}

func TestRecordedInstallationRequiresRuntimeFiles(t *testing.T) {
	root := t.TempDir()
	installation := Installation{
		Kind: "legacy", Root: filepath.Join(root, "removed"),
		DatabaseMode: "internal", Version: "source",
	}
	if validInstallation(installation) {
		t.Fatal("missing deployment directory was accepted")
	}
	if err := os.MkdirAll(installation.Root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", "compose.yaml"} {
		if err := os.WriteFile(filepath.Join(installation.Root, name), []byte("test\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if validInstallation(installation) {
		t.Fatal("internal database deployment without its compose overlay was accepted")
	}
	if err := os.WriteFile(filepath.Join(installation.Root, "compose.database.yaml"), []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !validInstallation(installation) {
		t.Fatal("complete deployment files were rejected")
	}
}
