package api

import (
	"cloudbrowser/internal/auth"
	"cloudbrowser/internal/protocol"
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Principal struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Admin bool   `json:"admin"`
	CSRF  string `json:"csrf"`
	Hash  string `json:"-"`
}
type contextKey struct{}
type stream struct {
	session, hash, lease string
	cancel               context.CancelFunc
	view                 bool
}
type API struct {
	db        *pgxpool.Pool
	origin    string
	transport *http.Transport
	client    *http.Client
	mu        sync.Mutex
	streams   map[string]stream
	rates     map[string][]time.Time
}

func New(db *pgxpool.Pool, origin, socket string) *API {
	t := protocol.UnixTransport(socket)
	return &API{db: db, origin: strings.TrimSuffix(origin, "/"), transport: t, client: &http.Client{Transport: t, Timeout: 90 * time.Second}, streams: map[string]stream{}, rates: map[string][]time.Time{}}
}
func (a *API) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.streams {
		s.cancel()
	}
	a.transport.CloseIdleConnections()
}
func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if a.db.Ping(r.Context()) != nil {
			protocol.Error(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		protocol.JSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/v1/auth/login", a.login)
	m.HandleFunc("POST /api/v1/auth/activate", a.activate)
	register := func(pattern string, h http.HandlerFunc) { m.Handle(pattern, a.protect(h)) }
	register("POST /api/v1/auth/logout", a.logout)
	register("POST /api/v1/auth/password", a.changePassword)
	register("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) { protocol.JSON(w, 200, who(r)) })
	register("GET /api/v1/browser", a.browser)
	register("POST /api/v1/browser/{action}", a.action)
	register("GET /api/v1/operations/{id}", a.operation)
	register("GET /api/v1/files", a.files)
	register("POST /api/v1/files", a.files)
	register("GET /api/v1/files/{id}/download", a.files)
	register("DELETE /api/v1/files/{id}", a.files)
	register("GET /api/v1/admin/users", a.adminUsers)
	register("POST /api/v1/admin/invites", a.invite)
	register("POST /api/v1/admin/users/{id}/{action}", a.adminAction)
	register("GET /api/v1/admin/resources", a.resources)
	register("GET /api/v1/admin/metrics", a.metrics)
	register("GET /view/{lease}/{rest...}", a.view)
	register("GET /audio/{lease}", a.audio)
	register("POST /control/{lease}/input", func(w http.ResponseWriter, r *http.Request) {
		if !a.lease(r) {
			protocol.Error(w, 409, "CONTROL_REVOKED")
			return
		}
		a.proxy(w, r, "/containers/"+who(r).ID+"/agent/input")
	})
	register("GET /control/{lease}/status", func(w http.ResponseWriter, r *http.Request) {
		if !a.lease(r) {
			protocol.Error(w, 409, "CONTROL_REVOKED")
			return
		}
		protocol.JSON(w, 200, map[string]bool{"ok": true})
	})
	register("GET /api/v1/events", a.events)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != a.origin {
			protocol.Error(w, 403, "ORIGIN_REJECTED")
			return
		}
		m.ServeHTTP(w, r)
	})
}
func who(r *http.Request) Principal { return r.Context().Value(contextKey{}).(Principal) }
func (a *API) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie("cb_session")
		if e != nil {
			protocol.Error(w, 401, "UNAUTHENTICATED")
			return
		}
		p := Principal{Hash: auth.Digest(c.Value)}
		e = a.db.QueryRow(r.Context(), `SELECT u.id,u.email,u.admin,s.csrf FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now() AND NOT u.disabled`, p.Hash).Scan(&p.ID, &p.Email, &p.Admin, &p.CSRF)
		if e != nil {
			protocol.Error(w, 401, "UNAUTHENTICATED")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && !auth.Equal(r.Header.Get("X-CSRF-Token"), p.CSRF) {
			protocol.Error(w, 403, "CSRF_REJECTED")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, p)))
	})
}
func (a *API) limited(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, values := range a.rates {
		if len(values) == 0 || now.Sub(values[len(values)-1]) > time.Minute {
			delete(a.rates, k)
		}
	}
	v := a.rates[key]
	var fresh []time.Time
	for _, t := range v {
		if now.Sub(t) < time.Minute {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= 10 {
		return true
	}
	a.rates[key] = append(fresh, now)
	return false
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var b struct{ Email, Password string }
	if !protocol.Decode(w, r, &b) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(b.Email))
	if a.limited("login-global") || a.limited("login:"+email) {
		protocol.Error(w, 429, "RATE_LIMITED")
		return
	}
	var id, hash string
	e := a.db.QueryRow(r.Context(), "SELECT id,password_hash FROM users WHERE email=$1 AND NOT disabled", email).Scan(&id, &hash)
	if e != nil || !auth.Verify(hash, b.Password) {
		protocol.Error(w, 401, "INVALID_CREDENTIALS")
		return
	}
	a.newLogin(w, r, id)
}
func (a *API) newLogin(w http.ResponseWriter, r *http.Request, id string) {
	token, csrf := auth.Token(), auth.Token()
	_, e := a.db.Exec(r.Context(), "INSERT INTO auth_sessions(token_hash,user_id,csrf,expires_at) VALUES($1,$2,$3,now()+interval '7 days')", auth.Digest(token), id, csrf)
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "cb_session", Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.origin, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: 604800})
	protocol.JSON(w, 200, map[string]string{"csrf": csrf})
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	_, e := a.db.Exec(r.Context(), "DELETE FROM auth_sessions WHERE token_hash=$1", p.Hash)
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	a.revoke(p.ID, p.Hash)
	http.SetCookie(w, &http.Cookie{Name: "cb_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(a.origin, "https://"), SameSite: http.SameSiteStrictMode})
	protocol.JSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !protocol.Decode(w, r, &body) {
		return
	}
	if a.limited("password:" + p.ID) {
		protocol.Error(w, 429, "RATE_LIMITED")
		return
	}
	var currentHash string
	if err := a.db.QueryRow(r.Context(), "SELECT password_hash FROM users WHERE id=$1", p.ID).Scan(&currentHash); err != nil || !auth.Verify(currentHash, body.CurrentPassword) {
		protocol.Error(w, 401, "CURRENT_PASSWORD_INVALID")
		return
	}
	if auth.Verify(currentHash, body.NewPassword) {
		protocol.Error(w, 409, "PASSWORD_UNCHANGED")
		return
	}
	newHash, err := auth.Password(body.NewPassword)
	if err != nil {
		protocol.Error(w, 400, "PASSWORD_LENGTH")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "UPDATE users SET password_hash=$2 WHERE id=$1", p.ID, newHash); err == nil {
		_, err = tx.Exec(r.Context(), "DELETE FROM auth_sessions WHERE user_id=$1 AND token_hash<>$2", p.ID, p.Hash)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	a.revokeOtherSessions(p.ID, p.Hash)
	a.audit(r.Context(), p.ID, "account.password_changed")
	protocol.JSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) activate(w http.ResponseWriter, r *http.Request) {
	var b struct{ Token, Email, Password string }
	if !protocol.Decode(w, r, &b) {
		return
	}
	if a.limited("activate") {
		protocol.Error(w, 429, "RATE_LIMITED")
		return
	}
	email := strings.ToLower(strings.TrimSpace(b.Email))
	if !strings.Contains(email, "@") || len(email) > 254 {
		protocol.Error(w, 400, "INVALID_EMAIL")
		return
	}
	hash, e := auth.Password(b.Password)
	if e != nil {
		protocol.Error(w, 400, "PASSWORD_LENGTH")
		return
	}
	tx, e := a.db.Begin(r.Context())
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	defer tx.Rollback(r.Context())
	res, e := tx.Exec(r.Context(), "UPDATE invites SET used_at=now() WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now()", auth.Digest(b.Token))
	if e != nil || res.RowsAffected() != 1 {
		protocol.Error(w, 400, "INVALID_INVITE")
		return
	}
	id := auth.ID()
	_, e = tx.Exec(r.Context(), "INSERT INTO users(id,email,password_hash) VALUES($1,$2,$3)", id, email, hash)
	if e == nil {
		_, e = tx.Exec(r.Context(), "INSERT INTO profiles(id,user_id) VALUES($1,$1)", id)
	}
	if e == nil {
		_, e = tx.Exec(r.Context(), "INSERT INTO sessions(id,profile_id) VALUES($1,$1)", id)
	}
	if e != nil {
		protocol.Error(w, 409, "ACCOUNT_CREATION_FAILED")
		return
	}
	if tx.Commit(r.Context()) != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	a.newLogin(w, r, id)
}
func (a *API) browser(w http.ResponseWriter, r *http.Request) {
	var state, msg string
	var last time.Time
	e := a.db.QueryRow(r.Context(), "SELECT state,error,last_connected_at FROM sessions WHERE id=$1", who(r).ID).Scan(&state, &msg, &last)
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	protocol.JSON(w, 200, map[string]any{"id": who(r).ID, "state": state, "error": msg, "lastConnectedAt": last, "idleSeconds": 600})
}
func (a *API) action(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	kind := r.PathValue("action")
	if kind == "takeover" {
		a.takeover(w, r)
		return
	}
	if kind != "start" && kind != "open" && kind != "stop" {
		protocol.Error(w, 404, "NOT_FOUND")
		return
	}
	var b struct {
		URL string `json:"url"`
	}
	if !protocol.Decode(w, r, &b) {
		return
	}
	if kind == "open" && protocol.ValidateURL(b.URL) != nil {
		protocol.Error(w, 400, "INVALID_URL")
		return
	}
	if kind != "open" {
		b.URL = ""
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		protocol.Error(w, 400, "IDEMPOTENCY_KEY_REQUIRED")
		return
	}
	id := auth.ID()
	_, e := a.db.Exec(r.Context(), "INSERT INTO operations(id,user_id,session_id,idem_key,kind,url,request_hash) VALUES($1,$2,$2,$3,$4,$5,$6) ON CONFLICT(user_id,idem_key) DO NOTHING", id, p.ID, key, kind, b.URL, auth.Digest(kind+"\n"+b.URL))
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	var oldKind, oldHash string
	e = a.db.QueryRow(r.Context(), "SELECT id,kind,request_hash FROM operations WHERE user_id=$1 AND idem_key=$2", p.ID, key).Scan(&id, &oldKind, &oldHash)
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	if oldKind != kind || oldHash != auth.Digest(kind+"\n"+b.URL) {
		protocol.Error(w, 409, "IDEMPOTENCY_CONFLICT")
		return
	}
	protocol.JSON(w, 202, map[string]string{"operationId": id})
}
func (a *API) operation(w http.ResponseWriter, r *http.Request) {
	var id, state, msg, sid string
	e := a.db.QueryRow(r.Context(), "SELECT id,state,error,session_id FROM operations WHERE id=$1 AND user_id=$2", r.PathValue("id"), who(r).ID).Scan(&id, &state, &msg, &sid)
	if e != nil {
		protocol.Error(w, 404, "NOT_FOUND")
		return
	}
	protocol.JSON(w, 200, map[string]string{"id": id, "state": state, "error": msg, "sessionId": sid})
}
func (a *API) takeover(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	token := auth.Token()
	tx, e := a.db.Begin(r.Context())
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	defer tx.Rollback(r.Context())
	var state string
	e = tx.QueryRow(r.Context(), "SELECT state FROM sessions WHERE id=$1 FOR UPDATE", p.ID).Scan(&state)
	if e != nil || state != "RUNNING" {
		protocol.Error(w, 409, "BROWSER_NOT_RUNNING")
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO controller_leases(session_id,auth_hash,token_hash) VALUES($1,$2,$3) ON CONFLICT(session_id) DO UPDATE SET auth_hash=$2,token_hash=$3,generation=controller_leases.generation+1,updated_at=now()`, p.ID, p.Hash, auth.Digest(token))
	if e != nil || tx.Commit(r.Context()) != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	a.revoke(p.ID, "")
	a.audit(r.Context(), p.ID, "controller.takeover")
	protocol.JSON(w, 200, map[string]string{"lease": token, "viewerUrl": "/view/" + token + "/vnc.html?autoconnect=1&resize=scale&path=view/" + token + "/websockify", "audioUrl": "/audio/" + token})
}
func (a *API) revoke(id, hash string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.streams {
		if s.session == id && (hash == "" || s.hash == hash) {
			s.cancel()
		}
	}
}
func (a *API) revokeOtherSessions(id, keepHash string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, stream := range a.streams {
		if stream.session == id && stream.hash != keepHash {
			stream.cancel()
		}
	}
}
func (a *API) lease(r *http.Request) bool {
	var ok bool
	p := who(r)
	e := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM controller_leases l JOIN sessions s ON s.id=l.session_id WHERE l.session_id=$1 AND l.auth_hash=$2 AND l.token_hash=$3 AND s.state='RUNNING')`, p.ID, p.Hash, auth.Digest(r.PathValue("lease"))).Scan(&ok)
	return e == nil && ok
}
func (a *API) view(w http.ResponseWriter, r *http.Request) {
	if !a.lease(r) {
		protocol.Error(w, 409, "CONTROL_REVOKED")
		return
	}
	path := "/containers/" + who(r).ID + "/view/" + r.PathValue("rest")
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		a.socket(w, r, path, true)
		return
	}
	a.proxy(w, r, path)
}
func (a *API) audio(w http.ResponseWriter, r *http.Request) {
	if !a.lease(r) {
		protocol.Error(w, 409, "CONTROL_REVOKED")
		return
	}
	a.socket(w, r, "/containers/"+who(r).ID+"/agent/audio", false)
}
func (a *API) proxy(w http.ResponseWriter, r *http.Request, path string) {
	p := &httputil.ReverseProxy{Transport: a.transport, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(&url.URL{Scheme: "http", Host: "runner"})
		pr.Out.URL.Path = path
		pr.Out.URL.RawPath = ""
		pr.Out.Header.Del("Cookie")
		pr.Out.Header.Del("Authorization")
		pr.Out.Header.Del("X-CSRF-Token")
		pr.Out.Header.Del("Origin")
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { protocol.Error(w, 502, "RUNNER_UNAVAILABLE") }}
	p.ServeHTTP(w, r)
}
func (a *API) socket(w http.ResponseWriter, r *http.Request, path string, view bool) {
	if r.Header.Get("Origin") != a.origin {
		protocol.Error(w, 403, "ORIGIN_REJECTED")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	p := who(r)
	id := auth.ID()
	a.mu.Lock()
	a.streams[id] = stream{p.ID, p.Hash, auth.Digest(r.PathValue("lease")), cancel, view}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.streams, id); a.mu.Unlock() }()
	// Recheck after registering so takeover cannot miss a just-opening connection.
	if !a.lease(r) {
		protocol.Error(w, 409, "CONTROL_REVOKED")
		return
	}
	up, _, e := websocket.Dial(ctx, "ws://runner"+path+query(r), &websocket.DialOptions{HTTPClient: &http.Client{Transport: a.transport}, Subprotocols: strings.Fields(strings.ReplaceAll(r.Header.Get("Sec-WebSocket-Protocol"), ",", " "))})
	if e != nil {
		protocol.Error(w, 502, "STREAM_UNAVAILABLE")
		return
	}
	defer up.CloseNow()
	down, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{up.Subprotocol()}})
	if e != nil {
		return
	}
	defer down.CloseNow()
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				c, stop := context.WithTimeout(ctx, 5*time.Second)
				e := down.Ping(c)
				stop()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	up.SetReadLimit(16 << 20)
	down.SetReadLimit(4 << 20)
	go func() { <-ctx.Done(); _ = up.CloseNow(); _ = down.CloseNow() }()
	errs := make(chan error, 2)
	copyWS := func(dst, src *websocket.Conn) {
		for {
			t, b, e := src.Read(ctx)
			if e != nil {
				errs <- e
				return
			}
			c, stop := context.WithTimeout(ctx, 5*time.Second)
			e = dst.Write(c, t, b)
			stop()
			if e != nil {
				errs <- e
				return
			}
		}
	}
	go copyWS(down, up)
	go copyWS(up, down)
	<-errs
}
func query(r *http.Request) string {
	if r.URL.RawQuery != "" {
		return "?" + r.URL.RawQuery
	}
	return ""
}
func (a *API) files(w http.ResponseWriter, r *http.Request) {
	path := "/containers/" + who(r).ID + "/files"
	if id := r.PathValue("id"); id != "" {
		path += "/" + id
		if r.Method == "GET" {
			path += "/download"
		}
	}
	a.proxy(w, r, path)
}
func (a *API) audit(ctx context.Context, id, event string) {
	_, _ = a.db.Exec(ctx, "INSERT INTO audit_events(user_id,event) VALUES($1,$2)", id, event)
}
func (a *API) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	f, ok := w.(http.Flusher)
	if !ok {
		return
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		var allowed bool
		if a.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM auth_sessions a JOIN users u ON u.id=a.user_id WHERE a.token_hash=$1 AND a.expires_at>now() AND NOT u.disabled)", who(r).Hash).Scan(&allowed) != nil || !allowed {
			return
		}
		var state string
		if a.db.QueryRow(r.Context(), "SELECT state FROM sessions WHERE id=$1", who(r).ID).Scan(&state) != nil {
			return
		}
		data, _ := json.Marshal(map[string]string{"state": state})
		_, e := io.WriteString(w, "data: "+string(data)+"\n\n")
		if e != nil {
			return
		}
		f.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
func (a *API) adminUsers(w http.ResponseWriter, r *http.Request) {
	if !who(r).Admin {
		protocol.Error(w, 403, "FORBIDDEN")
		return
	}
	rows, e := a.db.Query(r.Context(), "SELECT u.id,u.email,u.admin,u.disabled,s.state FROM users u JOIN sessions s ON s.id=u.id ORDER BY u.created_at")
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, email, state string
		var admin, disabled bool
		if rows.Scan(&id, &email, &admin, &disabled, &state) != nil {
			protocol.Error(w, 500, "DATABASE_ERROR")
			return
		}
		out = append(out, map[string]any{"id": id, "email": email, "admin": admin, "disabled": disabled, "state": state})
	}
	protocol.JSON(w, 200, out)
}
func (a *API) invite(w http.ResponseWriter, r *http.Request) {
	if !who(r).Admin {
		protocol.Error(w, 403, "FORBIDDEN")
		return
	}
	token := auth.Token()
	_, e := a.db.Exec(r.Context(), "INSERT INTO invites(token_hash,created_by,expires_at) VALUES($1,$2,now()+interval '48 hours')", auth.Digest(token), who(r).ID)
	if e != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	protocol.JSON(w, 201, map[string]string{"url": a.origin + "/#invite=" + token})
}
func (a *API) adminAction(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	id := r.PathValue("id")
	if !p.Admin {
		protocol.Error(w, 403, "FORBIDDEN")
		return
	}
	if !protocol.ValidID(id) {
		protocol.Error(w, 400, "INVALID_ID")
		return
	}
	kind := r.PathValue("action")
	if kind == "disable" || kind == "enable" {
		if id == p.ID {
			protocol.Error(w, 409, "CANNOT_DISABLE_SELF")
			return
		}
		res, e := a.db.Exec(r.Context(), "UPDATE users SET disabled=$2 WHERE id=$1", id, kind == "disable")
		if e != nil || res.RowsAffected() == 0 {
			protocol.Error(w, 404, "NOT_FOUND")
			return
		}
		if kind == "disable" {
			_, _ = a.db.Exec(r.Context(), "DELETE FROM auth_sessions WHERE user_id=$1", id)
			a.revoke(id, "")
		}
	} else if kind != "stop" {
		protocol.Error(w, 404, "NOT_FOUND")
		return
	}
	if kind != "enable" {
		_, e := a.db.Exec(r.Context(), "INSERT INTO operations(id,user_id,session_id,idem_key,kind) VALUES($1,$2,$2,$1,'stop')", auth.ID(), id)
		if e != nil {
			protocol.Error(w, 500, "DATABASE_ERROR")
			return
		}
	}
	a.audit(r.Context(), p.ID, "admin."+kind)
	protocol.JSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) resources(w http.ResponseWriter, r *http.Request) {
	if !who(r).Admin {
		protocol.Error(w, 403, "FORBIDDEN")
		return
	}
	a.proxy(w, r, "/resources")
}

func (a *API) metrics(w http.ResponseWriter, r *http.Request) {
	if !who(r).Admin {
		protocol.Error(w, 403, "FORBIDDEN")
		return
	}
	var running, failed int
	var operations, errors int64
	var average float64
	if a.db.QueryRow(r.Context(), "SELECT count(*) FILTER (WHERE state='RUNNING'),count(*) FILTER (WHERE state='FAILED') FROM sessions").Scan(&running, &failed) != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	if a.db.QueryRow(r.Context(), "SELECT count(*),count(*) FILTER (WHERE state IN ('FAILED','UNKNOWN')),COALESCE(avg(extract(epoch FROM updated_at-created_at)) FILTER (WHERE kind='start' AND state='SUCCEEDED'),0)::float8 FROM operations WHERE created_at>now()-interval '1 hour'").Scan(&operations, &errors, &average) != nil {
		protocol.Error(w, 500, "DATABASE_ERROR")
		return
	}
	protocol.JSON(w, 200, map[string]any{"runningSessions": running, "failedSessions": failed, "operationsLastHour": operations, "failedOrUnknownLastHour": errors, "averageStartOperationSeconds": average})
}
