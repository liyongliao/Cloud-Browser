package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

func (a *API) runner(ctx context.Context, method, path string, b any) (map[string]any, error) {
	var body io.Reader
	if b != nil {
		raw, _ := json.Marshal(b)
		body = bytes.NewReader(raw)
	}
	r, e := http.NewRequestWithContext(ctx, method, "http://runner"+path, body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	resp, e := a.client.Do(r)
	if e != nil {
		return nil, fmt.Errorf("RUNNER_UNAVAILABLE")
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode >= 300 {
		if code, ok := out["error"].(string); ok {
			return out, fmt.Errorf("%s", code)
		}
		return out, fmt.Errorf("RUNNER_ERROR")
	}
	return out, nil
}
func (a *API) Work(ctx context.Context) {
	// Hold one dedicated PostgreSQL advisory lock for the entire scheduler lifetime.
	conn, e := a.db.Acquire(ctx)
	if e != nil {
		log.Print("worker database unavailable")
		return
	}
	defer conn.Release()
	var locked bool
	if conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(731924)").Scan(&locked) != nil || !locked {
		log.Print("worker lock unavailable")
		return
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(731924)")
	go a.trackConnections(ctx)
	a.reconcile(ctx)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if conn.Ping(ctx) != nil {
				log.Print("worker lost database lock connection; stopping scheduler")
				return
			}
			a.next(ctx)
			a.idle(ctx)
		}
	}
}
func (a *API) next(ctx context.Context) {
	var id, uid, kind, target, state string
	e := a.db.QueryRow(ctx, "SELECT id,user_id,kind,url,state FROM operations WHERE state IN ('QUEUED','RUNNING') ORDER BY created_at LIMIT 1").Scan(&id, &uid, &kind, &target, &state)
	if e != nil {
		return
	}
	var disabled bool
	if a.db.QueryRow(ctx, "SELECT disabled FROM users WHERE id=$1", uid).Scan(&disabled) != nil {
		return
	}
	if disabled && kind != "stop" {
		a.finish(ctx, id, "FAILED", "ACCOUNT_DISABLED")
		return
	}
	startedAt := time.Now()
	defer func() {
		log.Printf("operation=%s kind=%s duration_ms=%d", id, kind, time.Since(startedAt).Milliseconds())
	}()
	if _, e = a.db.Exec(ctx, "UPDATE operations SET state='RUNNING',updated_at=now() WHERE id=$1", id); e != nil {
		return
	}
	c, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	if kind == "stop" {
		a.revoke(uid, "")
		a.setState(ctx, uid, "STOPPING", "")
		stopResult, stopErr := a.runner(c, "POST", "/containers/"+uid+"/stop", nil)
		e = stopErr
		if stopResult["forced"] == true {
			a.audit(ctx, uid, "browser.forced_stop")
		}
		if e != nil {
			a.setState(ctx, uid, "FAILED", e.Error())
			a.finish(ctx, id, "FAILED", e.Error())
			return
		}
		a.setState(ctx, uid, "STOPPED", "")
		a.finish(ctx, id, "SUCCEEDED", "")
		return
	}
	var current string
	if a.db.QueryRow(ctx, "SELECT state FROM sessions WHERE id=$1", uid).Scan(&current) != nil {
		return
	}
	if current != "RUNNING" {
		a.setState(ctx, uid, "STARTING", "")
	}
	_, e = a.runner(c, "POST", "/containers/"+uid+"/start", nil)
	if e != nil {
		a.setState(ctx, uid, "FAILED", e.Error())
		a.finish(ctx, id, "FAILED", e.Error())
		return
	}
	if current != "RUNNING" {
		_, _ = a.db.Exec(ctx, "UPDATE sessions SET last_connected_at=now() WHERE id=$1", uid)
	}
	a.setState(ctx, uid, "RUNNING", "")
	if kind == "open" {
		_, e = a.runner(c, "POST", "/containers/"+uid+"/agent/open", map[string]string{"operationId": id, "url": target})
		if e != nil {
			result, checkErr := a.runner(c, "GET", "/containers/"+uid+"/agent/operations/"+id, nil)
			if checkErr == nil && result["state"] == "SUCCEEDED" {
				a.finish(ctx, id, "SUCCEEDED", "")
				return
			}
			a.finish(ctx, id, "UNKNOWN", "OPEN_RESULT_UNKNOWN")
			return
		}
	}
	a.finish(ctx, id, "SUCCEEDED", "")
}
func (a *API) setState(ctx context.Context, id, state, msg string) {
	if state == "STOPPING" || state == "STOPPED" || state == "FAILED" {
		a.revoke(id, "")
		_, _ = a.db.Exec(ctx, "DELETE FROM controller_leases WHERE session_id=$1", id)
	}
	_, _ = a.db.Exec(ctx, "UPDATE sessions SET state=$2,error=$3,updated_at=now() WHERE id=$1", id, state, msg)
	log.Printf("session=%s state=%s", id, state)
}
func (a *API) finish(ctx context.Context, id, state, msg string) {
	_, _ = a.db.Exec(ctx, "UPDATE operations SET state=$2,error=$3,url=CASE WHEN $2 IN ('SUCCEEDED','FAILED','UNKNOWN') THEN '' ELSE url END,updated_at=now() WHERE id=$1", id, state, msg)
}
func (a *API) idle(ctx context.Context) {
	rows, e := a.db.Query(ctx, "SELECT id FROM sessions WHERE state='RUNNING' AND last_connected_at<now()-interval '10 minutes'")
	if e != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		a.mu.Lock()
		active := false
		for _, s := range a.streams {
			if s.session == id && s.view {
				active = true
				break
			}
		}
		a.mu.Unlock()
		if active {
			continue
		}
		a.revoke(id, "")
		a.setState(ctx, id, "STOPPING", "")
		_, e = a.runner(ctx, "POST", "/containers/"+id+"/stop", nil)
		if e != nil {
			a.setState(ctx, id, "FAILED", e.Error())
		} else {
			a.setState(ctx, id, "STOPPED", "")
		}
	}
	a.reconcile(ctx)
	_, _ = a.db.Exec(ctx, "DELETE FROM auth_sessions WHERE expires_at<now()")
	_, _ = a.db.Exec(ctx, "DELETE FROM operations WHERE state IN ('SUCCEEDED','FAILED','UNKNOWN') AND updated_at<now()-interval '7 days'")
}
func (a *API) reconcile(ctx context.Context) {
	out, e := a.runner(ctx, "GET", "/containers", nil)
	if e != nil {
		return
	}
	items, _ := out["containers"].([]any)
	actual := map[string]bool{}
	for _, v := range items {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		running, _ := m["running"].(bool)
		actual[id] = running
		var known bool
		if a.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE id=$1)", id).Scan(&known) != nil {
			continue
		}
		if !known {
			_, _ = a.runner(ctx, "POST", "/containers/"+id+"/stop", nil)
			continue
		}
		if running {
			var desired string
			if a.db.QueryRow(ctx, "SELECT state FROM sessions WHERE id=$1", id).Scan(&desired) == nil && (desired == "STOPPED" || desired == "FAILED" || desired == "STOPPING") {
				a.revoke(id, "")
				if _, e := a.runner(ctx, "POST", "/containers/"+id+"/stop", nil); e == nil {
					actual[id] = false
					a.setState(ctx, id, "STOPPED", "")
				}
			}
		}
	}
	rows, e := a.db.Query(ctx, "SELECT id,state FROM sessions WHERE state IN ('RUNNING','STARTING','STOPPING')")
	if e != nil {
		return
	}
	type entry struct{ id, state string }
	var entries []entry
	for rows.Next() {
		var x entry
		if rows.Scan(&x.id, &x.state) == nil {
			entries = append(entries, x)
		}
	}
	rows.Close()
	for _, x := range entries {
		if !actual[x.id] {
			a.revoke(x.id, "")
			if x.state == "STOPPING" {
				a.setState(ctx, x.id, "STOPPED", "")
			} else {
				a.setState(ctx, x.id, "FAILED", "CONTAINER_NOT_RUNNING")
			}
		}
	}
}
func (a *API) trackConnections(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		copy := make([]stream, 0, len(a.streams))
		for _, s := range a.streams {
			copy = append(copy, s)
		}
		a.mu.Unlock()
		for _, s := range copy {
			var ok bool
			e := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM controller_leases l JOIN auth_sessions a ON a.token_hash=l.auth_hash JOIN users u ON u.id=a.user_id JOIN sessions s ON s.id=l.session_id WHERE l.session_id=$1 AND l.auth_hash=$2 AND l.token_hash=$3 AND a.expires_at>now() AND NOT u.disabled AND s.state='RUNNING')`, s.session, s.hash, s.lease).Scan(&ok)
			if e != nil || !ok {
				s.cancel()
				continue
			}
			if s.view {
				_, _ = a.db.Exec(ctx, "UPDATE sessions SET last_connected_at=now() WHERE id=$1", s.session)
			}
		}
	}
}
