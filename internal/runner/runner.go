package runner

import (
	"cloudbrowser/internal/auth"
	"cloudbrowser/internal/files"
	"cloudbrowser/internal/protocol"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Runner struct {
	Root, Image, Network string
	Max                  int
	mu                   sync.Mutex
}
type container struct {
	ID       string `json:"id"`
	Running  bool   `json:"running"`
	IP       string `json:"-"`
	Secret   string `json:"-"`
	ExitCode int    `json:"exitCode"`
}

func New() *Runner {
	max, _ := strconv.Atoi(protocol.Env("MAX_SESSIONS", "3"))
	if max < 1 {
		max = 3
	}
	return &Runner{Root: protocol.Env("PROFILE_ROOT", "/srv/cloud-browser/profiles"), Image: protocol.Env("BROWSER_IMAGE", "cloud-browser-browser:local"), Network: "cloud-browser-egress", Max: max}
}
func (r *Runner) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /resources", r.resources)
	m.HandleFunc("GET /containers", r.list)
	m.HandleFunc("POST /containers/{id}/start", r.start)
	m.HandleFunc("POST /containers/{id}/stop", r.stop)
	m.HandleFunc("/containers/{id}/view/{rest...}", r.proxy)
	m.HandleFunc("/containers/{id}/agent/{rest...}", r.proxy)
	for _, pattern := range []string{"GET /containers/{id}/files", "POST /containers/{id}/files", "GET /containers/{id}/files/{file}/download", "DELETE /containers/{id}/files/{file}"} {
		m.HandleFunc(pattern, func(w http.ResponseWriter, q *http.Request) {
			if !protocol.ValidID(q.PathValue("id")) {
				protocol.Error(w, 400, "INVALID_ID")
				return
			}
			files.Handler(filepath.Join(r.Root, q.PathValue("id")))(w, q)
		})
	}
	return m
}
func docker(ctx context.Context, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "docker", args...)
	b, e := c.Output()
	return b, e
}
func (r *Runner) inspect(ctx context.Context, id string) (container, error) {
	var out []struct {
		State struct {
			Running  bool
			ExitCode int
		}
		Config struct {
			Env    []string
			Labels map[string]string
		}
		NetworkSettings struct {
			Networks map[string]struct{ IPAddress string }
		}
	}
	b, e := docker(ctx, "inspect", "cb-"+id)
	if e != nil {
		return container{}, e
	}
	if json.Unmarshal(b, &out) != nil || len(out) != 1 || out[0].Config.Labels["cloud-browser.profile"] != id {
		return container{}, fmt.Errorf("INVALID_CONTAINER")
	}
	c := container{ID: id, Running: out[0].State.Running, IP: out[0].NetworkSettings.Networks[r.Network].IPAddress, ExitCode: out[0].State.ExitCode}
	for _, v := range out[0].Config.Env {
		if strings.HasPrefix(v, "AGENT_TOKEN=") {
			c.Secret = strings.TrimPrefix(v, "AGENT_TOKEN=")
		}
	}
	return c, nil
}
func (r *Runner) all(ctx context.Context) ([]container, error) {
	b, e := docker(ctx, "ps", "-a", "--filter", "label=cloud-browser.managed=true", "--format", "{{.Label \"cloud-browser.profile\"}}")
	if e != nil {
		return nil, e
	}
	out := []container{}
	for _, id := range strings.Fields(string(b)) {
		if !protocol.ValidID(id) {
			continue
		}
		c, e := r.inspect(ctx, id)
		if e == nil {
			out = append(out, c)
		}
	}
	return out, nil
}
func (r *Runner) list(w http.ResponseWriter, q *http.Request) {
	c, e := r.all(q.Context())
	if e != nil {
		protocol.Error(w, 503, "DOCKER_UNAVAILABLE")
		return
	}
	protocol.JSON(w, 200, map[string]any{"containers": c})
}
func (r *Runner) resources(w http.ResponseWriter, q *http.Request) {
	all, e := r.all(q.Context())
	if e != nil {
		protocol.Error(w, 503, "DOCKER_UNAVAILABLE")
		return
	}
	n := 0
	for _, c := range all {
		if c.Running {
			n++
		}
	}
	free, e := files.DiskFree(r.Root)
	if e != nil {
		protocol.Error(w, 503, "STORAGE_UNAVAILABLE")
		return
	}
	protocol.JSON(w, 200, map[string]any{"running": n, "maxSessions": r.Max, "diskFreeFraction": free, "memoryAvailableBytes": memoryAvailable(), "diskLow": free < 0.15})
}
func memoryAvailable() int64 {
	b, e := os.ReadFile("/proc/meminfo")
	if e != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && f[0] == "MemAvailable:" {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			return n * 1024
		}
	}
	return -1
}
func (r *Runner) start(w http.ResponseWriter, q *http.Request) {
	id := q.PathValue("id")
	if !protocol.ValidID(id) {
		protocol.Error(w, 400, "INVALID_ID")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx := q.Context()
	if c, e := r.inspect(ctx, id); e == nil && c.Running {
		if r.wait(ctx, c) != nil {
			protocol.Error(w, 503, "BROWSER_UNHEALTHY")
			return
		}
		protocol.JSON(w, 200, map[string]string{"state": "RUNNING"})
		return
	}
	all, e := r.all(ctx)
	if e != nil {
		protocol.Error(w, 503, "DOCKER_UNAVAILABLE")
		return
	}
	n := 0
	for _, c := range all {
		if c.Running {
			n++
		}
	}
	if n >= r.Max {
		protocol.Error(w, 409, "CAPACITY_FULL")
		return
	}
	if memoryAvailable() < 1536<<20 {
		protocol.Error(w, 503, "MEMORY_LOW")
		return
	}
	free, e := files.DiskFree(r.Root)
	if e != nil || free < 0.15 {
		protocol.Error(w, 507, "DISK_LOW")
		return
	}
	// The root is administrator-owned; each user controls only their own home.
	home := filepath.Join(r.Root, id)
	if e = os.MkdirAll(home, 0700); e != nil {
		protocol.Error(w, 500, "PROFILE_CREATE_FAILED")
		return
	}
	if info, e := os.Lstat(home); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		protocol.Error(w, 500, "INVALID_PROFILE")
		return
	}
	if e = os.Chown(home, 1000, 1000); e != nil {
		protocol.Error(w, 500, "PROFILE_OWNERSHIP_FAILED")
		return
	}
	root, e := os.OpenRoot(home)
	if e != nil {
		protocol.Error(w, 500, "INVALID_PROFILE")
		return
	}
	defer root.Close()
	for _, dir := range []string{"Uploads", "Downloads"} {
		if e = root.Mkdir(dir, 0700); e != nil && !os.IsExist(e) {
			protocol.Error(w, 500, "PROFILE_CREATE_FAILED")
			return
		}
		s, e := root.Lstat(dir)
		if e != nil || !s.IsDir() {
			protocol.Error(w, 500, "INVALID_PROFILE")
			return
		}
		if e = root.Chown(dir, 1000, 1000); e != nil {
			protocol.Error(w, 500, "PROFILE_OWNERSHIP_FAILED")
			return
		}
	}
	used, e := files.Usage(home)
	if e != nil || used >= files.SoftLimit {
		protocol.Error(w, 507, "QUOTA_EXCEEDED")
		return
	}
	if _, e = r.inspect(ctx, id); e == nil {
		if _, e = docker(ctx, "rm", "cb-"+id); e != nil {
			protocol.Error(w, 500, "CONTAINER_REMOVE_FAILED")
			return
		}
	}
	secret := auth.Token()
	args := []string{"run", "-d", "--name", "cb-" + id, "--hostname", "cloud-browser", "--label", "cloud-browser.managed=true", "--label", "cloud-browser.profile=" + id, "--network", r.Network, "--memory", "2g", "--memory-swap", "2g", "--cpus", "2", "--shm-size", "512m", "--pids-limit", "512", "--user", "1000:1000", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--security-opt", "seccomp=/etc/cloud-browser/chrome-seccomp.json", "--security-opt", "apparmor=cloud-browser", "--sysctl", "net.ipv6.conf.all.disable_ipv6=1", "--mount", "type=bind,src=" + home + ",dst=/home/browser", "--tmpfs", "/tmp:rw,nosuid,nodev,size=512m", "--read-only", "--env", "AGENT_TOKEN=" + secret, r.Image}
	if _, e = docker(ctx, args...); e != nil {
		protocol.Error(w, 500, "CONTAINER_START_FAILED")
		return
	}
	c, e := r.inspect(ctx, id)
	if e != nil || r.wait(ctx, c) != nil {
		_, _ = docker(context.WithoutCancel(ctx), "stop", "-t", "30", "cb-"+id)
		protocol.Error(w, 504, "BROWSER_START_TIMEOUT")
		return
	}
	protocol.JSON(w, 200, map[string]string{"state": "RUNNING"})
}
func (r *Runner) wait(ctx context.Context, c container) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		q, _ := http.NewRequestWithContext(ctx, "GET", "http://"+c.IP+":8081/healthz", nil)
		q.Header.Set("Authorization", "Bearer "+c.Secret)
		res, e := client.Do(q)
		if e == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
func (r *Runner) stop(w http.ResponseWriter, q *http.Request) {
	id := q.PathValue("id")
	if !protocol.ValidID(id) {
		protocol.Error(w, 400, "INVALID_ID")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, e := r.inspect(q.Context(), id)
	if e != nil {
		if _, e = r.all(q.Context()); e != nil {
			protocol.Error(w, 503, "DOCKER_UNAVAILABLE")
			return
		}
		protocol.JSON(w, 200, map[string]any{"state": "STOPPED", "forced": false})
		return
	}
	if c.Running {
		if _, e = docker(q.Context(), "stop", "--time", "30", "cb-"+id); e != nil {
			protocol.Error(w, 500, "STOP_FAILED")
			return
		}
	}
	c, e = r.inspect(q.Context(), id)
	if e != nil {
		protocol.Error(w, 500, "INSPECT_FAILED")
		return
	}
	forced := c.ExitCode == 137
	if _, e = docker(q.Context(), "rm", "cb-"+id); e != nil {
		protocol.Error(w, 500, "CONTAINER_REMOVE_FAILED")
		return
	}
	protocol.JSON(w, 200, map[string]any{"state": "STOPPED", "forced": forced})
}
func (r *Runner) proxy(w http.ResponseWriter, q *http.Request) {
	id := q.PathValue("id")
	if !protocol.ValidID(id) {
		protocol.Error(w, 400, "INVALID_ID")
		return
	}
	c, e := r.inspect(q.Context(), id)
	if e != nil || !c.Running || c.IP == "" {
		protocol.Error(w, 409, "BROWSER_NOT_RUNNING")
		return
	}
	view := strings.Contains(q.URL.Path, "/view/")
	port := "8081"
	if view {
		port = "6901"
	}
	p := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(&url.URL{Scheme: "http", Host: c.IP + ":" + port})
		pr.Out.URL.Path = "/" + q.PathValue("rest")
		pr.Out.URL.RawPath = ""
		pr.Out.Header.Del("Cookie")
		pr.Out.Header.Del("Origin")
		pr.Out.Header.Del("Authorization")
		if view {
			// KasmVNC requires Origin for its WebSocket handshake. The public
			// origin and controller are already validated by the API gateway.
			if strings.EqualFold(q.Header.Get("Upgrade"), "websocket") {
				pr.Out.Header.Set("Origin", "http://"+c.IP+":"+port)
			}
			pr.Out.SetBasicAuth("browser", c.Secret)
		} else {
			pr.Out.Header.Set("Authorization", "Bearer "+c.Secret)
		}
	}, ModifyResponse: func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		resp.Header.Del("WWW-Authenticate")
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { protocol.Error(w, 502, "BROWSER_UNAVAILABLE") }}
	p.ServeHTTP(w, q)
}
