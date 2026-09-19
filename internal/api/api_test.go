package api

import (
	"bytes"
	"cloudbrowser/internal/auth"
	"cloudbrowser/internal/protocol"
	"cloudbrowser/migrations"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	a        *API
	s        *httptest.Server
	db       *pgxpool.Pool
	client   *http.Client
	id, csrf string
	starts   atomic.Int32
	running  atomic.Bool
}

func fixtureNew(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL or run scripts/test-integration.sh")
	}
	ctx := context.Background()
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	schema := "t_" + auth.ID()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(ctx, migrations.SQL); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close(); admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	// Keep the socket path below Darwin's sockaddr_un limit.
	socketDir, e := os.MkdirTemp("/tmp", "cb-runner-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "r.sock")
	ln, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{db: db, id: auth.ID()}
	rs := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/view/websockify") {
			c, e := websocket.Accept(w, r, nil)
			if e != nil {
				return
			}
			defer c.CloseNow()
			for {
				typ, b, e := c.Read(r.Context())
				if e != nil {
					return
				}
				if c.Write(r.Context(), typ, b) != nil {
					return
				}
			}
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/start"):
			if !f.running.Swap(true) {
				f.starts.Add(1)
			}
		case strings.HasSuffix(r.URL.Path, "/stop"):
			f.running.Store(false)
		case r.URL.Path == "/containers":
			out := []map[string]any{}
			if f.running.Load() {
				out = append(out, map[string]any{"id": f.id, "running": true})
			}
			protocol.JSON(w, 200, map[string]any{"containers": out})
			return
		}
		protocol.JSON(w, 200, map[string]string{"state": "SUCCEEDED"})
	})}
	go rs.Serve(ln)
	t.Cleanup(func() { rs.Close() })
	f.a = New(db, "", socket)
	f.s = httptest.NewServer(f.a.Handler())
	f.a.origin = f.s.URL
	t.Cleanup(func() { f.a.Close(); f.s.Close() })
	jar, _ := cookiejar.New(nil)
	f.client = &http.Client{Jar: jar}
	hash, _ := auth.Password("correct test password")
	if _, e = db.Exec(ctx, "INSERT INTO users(id,email,password_hash,admin) VALUES($1,'admin@example.com',$2,true)", f.id, hash); e != nil {
		t.Fatal(e)
	}
	db.Exec(ctx, "INSERT INTO profiles(id,user_id) VALUES($1,$1)", f.id)
	db.Exec(ctx, "INSERT INTO sessions(id,profile_id) VALUES($1,$1)", f.id)
	code, out := f.req(t, "POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "correct test password"}, "")
	if code != 200 {
		t.Fatal(code, out)
	}
	f.csrf = out["csrf"].(string)
	return f
}
func (f *fixture) req(t *testing.T, method, path string, body any, key string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	q, _ := http.NewRequest(method, f.s.URL+path, bytes.NewReader(b))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Origin", f.s.URL)
	q.Header.Set("X-CSRF-Token", f.csrf)
	q.Header.Set("Idempotency-Key", key)
	res, e := f.client.Do(q)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	out := map[string]any{}
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}
func TestOperationsAndIsolation(t *testing.T) {
	f := fixtureNew(t)
	ctx := context.Background()
	code, op := f.req(t, "POST", "/api/v1/browser/open", map[string]string{"url": "https://example.com/a"}, "same-request")
	if code != 202 {
		t.Fatal(code, op)
	}
	_, again := f.req(t, "POST", "/api/v1/browser/open", map[string]string{"url": "https://example.com/a"}, "same-request")
	if op["operationId"] != again["operationId"] {
		t.Fatal("not idempotent")
	}
	f.a.next(ctx)
	_, completed := f.req(t, "GET", "/api/v1/operations/"+op["operationId"].(string), nil, "")
	if completed["state"] != "SUCCEEDED" {
		t.Fatal(completed)
	}
	code, again = f.req(t, "POST", "/api/v1/browser/open", map[string]string{"url": "https://example.com/a"}, "same-request")
	if code != 202 || again["operationId"] != op["operationId"] {
		t.Fatal("completed retry broken", code, again)
	}
	code, _ = f.req(t, "POST", "/api/v1/browser/open", map[string]string{"url": "https://example.com/b"}, "same-request")
	if code != 409 {
		t.Fatal("conflicting retry accepted")
	}
	if f.starts.Load() != 1 {
		t.Fatal("duplicate start")
	}
	f.req(t, "POST", "/api/v1/browser/start", map[string]string{}, "another-start")
	f.a.next(ctx)
	if f.starts.Load() != 1 {
		t.Fatal("running browser duplicated")
	}
	code, _ = f.req(t, "GET", "/api/v1/operations/"+auth.ID(), nil, "")
	if code != 404 {
		t.Fatal("operation privacy")
	}
	code, invite := f.req(t, "POST", "/api/v1/admin/invites", map[string]string{}, "")
	if code != 201 {
		t.Fatal(code)
	}
	token := strings.Split(invite["url"].(string), "#invite=")[1]
	jar, _ := cookiejar.New(nil)
	oldClient, oldCSRF := f.client, f.csrf
	f.client = &http.Client{Jar: jar}
	f.csrf = ""
	code, login := f.req(t, "POST", "/api/v1/auth/activate", map[string]string{"email": "second@example.com", "password": "another test password", "token": token}, "")
	if code != 200 {
		t.Fatal(code, login)
	}
	f.csrf = login["csrf"].(string)
	code, _ = f.req(t, "GET", "/api/v1/operations/"+op["operationId"].(string), nil, "")
	if code != 404 {
		t.Fatal("other user accessed operation")
	}
	code, _ = f.req(t, "GET", "/api/v1/admin/users", nil, "")
	if code != 403 {
		t.Fatal("non-admin accessed admin")
	}
	code, _ = f.req(t, "POST", "/api/v1/auth/activate", map[string]string{"email": "third@example.com", "password": "another test password", "token": token}, "")
	if code != 400 {
		t.Fatal("invite reused")
	}
	f.client, f.csrf = oldClient, oldCSRF
}
func TestCSRFAndLogout(t *testing.T) {
	f := fixtureNew(t)
	q, _ := http.NewRequest("POST", f.s.URL+"/api/v1/browser/start", strings.NewReader("{}"))
	q.Header.Set("Origin", f.s.URL)
	res, e := f.client.Do(q)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("csrf missing accepted")
	}
	q, _ = http.NewRequest("POST", f.s.URL+"/api/v1/auth/login", strings.NewReader("{}"))
	q.Header.Set("Origin", "https://evil.example")
	res, e = f.client.Do(q)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cross origin accepted")
	}
	code, _ := f.req(t, "POST", "/api/v1/auth/logout", map[string]string{}, "")
	if code != 200 {
		t.Fatal(code)
	}
	code, _ = f.req(t, "GET", "/api/v1/me", nil, "")
	if code != 401 {
		t.Fatal("logout failed")
	}
}

func TestPasswordChangeKeepsCurrentSessionAndRevokesOthers(t *testing.T) {
	f := fixtureNew(t)
	otherJar, _ := cookiejar.New(nil)
	otherClient := &http.Client{Jar: otherJar}
	body, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "correct test password"})
	request, _ := http.NewRequest("POST", f.s.URL+"/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", f.s.URL)
	response, err := otherClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("second login failed", response.StatusCode)
	}

	code, result := f.req(t, "POST", "/api/v1/auth/password", map[string]string{
		"currentPassword": "wrong password",
		"newPassword":     "a different secure password",
	}, "")
	if code != http.StatusUnauthorized || result["error"] != "CURRENT_PASSWORD_INVALID" {
		t.Fatal("wrong current password accepted", code, result)
	}
	code, result = f.req(t, "POST", "/api/v1/auth/password", map[string]string{
		"currentPassword": "correct test password",
		"newPassword":     "correct test password",
	}, "")
	if code != http.StatusConflict || result["error"] != "PASSWORD_UNCHANGED" {
		t.Fatal("unchanged password accepted", code, result)
	}
	code, result = f.req(t, "POST", "/api/v1/auth/password", map[string]string{
		"currentPassword": "correct test password",
		"newPassword":     "a different secure password",
	}, "")
	if code != http.StatusOK {
		t.Fatal("password change failed", code, result)
	}
	code, _ = f.req(t, "GET", "/api/v1/me", nil, "")
	if code != http.StatusOK {
		t.Fatal("current session was revoked")
	}
	request, _ = http.NewRequest("GET", f.s.URL+"/api/v1/me", nil)
	response, err = otherClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("other session remained active", response.StatusCode)
	}
	loginCode, _ := f.req(t, "POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "a different secure password"}, "")
	if loginCode != http.StatusOK {
		t.Fatal("new password cannot log in", loginCode)
	}
}
func TestTakeoverClosesOldWebSocket(t *testing.T) {
	f := fixtureNew(t)
	f.db.Exec(context.Background(), "UPDATE sessions SET state='RUNNING' WHERE id=$1", f.id)
	f.running.Store(true)
	code, l := f.req(t, "POST", "/api/v1/browser/takeover", map[string]string{}, "")
	if code != 200 {
		t.Fatal(code, l)
	}
	lease := l["lease"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.s.URL, "http")+"/view/"+lease+"/websockify", &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: http.Header{"Origin": []string{f.s.URL}}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseNow()
	if e = c.Write(ctx, websocket.MessageBinary, []byte("hello")); e != nil {
		t.Fatal(e)
	}
	_, b, e := c.Read(ctx)
	if e != nil || string(b) != "hello" {
		t.Fatal("not connected", e)
	}
	code, _ = f.req(t, "POST", "/api/v1/browser/takeover", map[string]string{}, "")
	if code != 200 {
		t.Fatal(code)
	}
	if _, _, e = c.Read(ctx); e == nil {
		t.Fatal("old controller remained connected")
	}
	code, _ = f.req(t, "GET", "/control/"+lease+"/status", nil, "")
	if code != 409 {
		t.Fatal("old lease accepted", code)
	}
}
func TestIdleAndRecovery(t *testing.T) {
	f := fixtureNew(t)
	ctx := context.Background()
	f.running.Store(true)
	f.db.Exec(ctx, "UPDATE sessions SET state='RUNNING',last_connected_at=now()-interval '11 minutes' WHERE id=$1", f.id)
	f.a.idle(ctx)
	var state string
	f.db.QueryRow(ctx, "SELECT state FROM sessions WHERE id=$1", f.id).Scan(&state)
	if state != "STOPPED" || f.running.Load() {
		t.Fatal(state)
	}
	f.db.Exec(ctx, "UPDATE sessions SET state='RUNNING' WHERE id=$1", f.id)
	f.a.reconcile(ctx)
	f.db.QueryRow(ctx, "SELECT state FROM sessions WHERE id=$1", f.id).Scan(&state)
	if state != "FAILED" {
		t.Fatal(fmt.Sprint("missing container not reconciled: ", state))
	}
}

func TestStoppedLeaseCannotRevive(t *testing.T) {
	f := fixtureNew(t)
	ctx := context.Background()
	f.a.setState(ctx, f.id, "RUNNING", "")
	code, l := f.req(t, "POST", "/api/v1/browser/takeover", map[string]string{}, "")
	if code != 200 {
		t.Fatal(code, l)
	}
	f.a.setState(ctx, f.id, "STOPPED", "")
	f.a.setState(ctx, f.id, "RUNNING", "")
	code, _ = f.req(t, "GET", "/control/"+l["lease"].(string)+"/status", nil, "")
	if code != 409 {
		t.Fatal("old controller revived after restart", code)
	}
}

func TestFileChooserRequiresCurrentLease(t *testing.T) {
	f := fixtureNew(t)
	f.a.setState(context.Background(), f.id, "RUNNING", "")
	code, lease := f.req(t, "POST", "/api/v1/browser/takeover", map[string]string{}, "")
	if code != 200 {
		t.Fatal(code, lease)
	}
	token := lease["lease"].(string)
	code, _ = f.req(t, "GET", "/control/"+token+"/file-chooser", nil, "")
	if code != 200 {
		t.Fatal("file chooser watch rejected", code)
	}
	code, _ = f.req(t, "POST", "/control/"+token+"/file-chooser/"+auth.ID(), map[string]any{"names": []string{"example.txt"}}, "")
	if code != 200 {
		t.Fatal("file chooser completion rejected", code)
	}
	code, _ = f.req(t, "POST", "/control/"+token+"/file-chooser/not-an-id", map[string]any{"names": []string{}}, "")
	if code != 400 {
		t.Fatal("invalid chooser id accepted", code)
	}
	_, _ = f.req(t, "POST", "/api/v1/browser/takeover", map[string]string{}, "")
	code, _ = f.req(t, "GET", "/control/"+token+"/file-chooser", nil, "")
	if code != 409 {
		t.Fatal("revoked lease kept file chooser access", code)
	}
}
