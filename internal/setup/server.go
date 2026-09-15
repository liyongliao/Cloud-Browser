package setup

import (
	"cloudbrowser/internal/auth"
	"cloudbrowser/migrations"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed ui/*
var content embed.FS

type Options struct {
	Token   string
	Root    string
	DryRun  bool
	Command func(context.Context, string, ...string) error
}

type Server struct {
	token   string
	root    string
	dryRun  bool
	command func(context.Context, string, ...string) error
	mu      sync.RWMutex
	status  Status
}

type Status struct {
	State    string `json:"state"`
	Step     string `json:"step"`
	Message  string `json:"message"`
	Progress int    `json:"progress"`
	Origin   string `json:"origin,omitempty"`
}

type Config struct {
	Domain           string `json:"domain"`
	GatewayMode      string `json:"gatewayMode"`
	DatabaseMode     string `json:"databaseMode"`
	DatabaseHost     string `json:"databaseHost"`
	DatabasePort     int    `json:"databasePort"`
	DatabaseName     string `json:"databaseName"`
	DatabaseUser     string `json:"databaseUser"`
	DatabasePassword string `json:"databasePassword"`
	DatabaseSSLMode  string `json:"databaseSSLMode"`
	AdminEmail       string `json:"adminEmail"`
	AdminPassword    string `json:"adminPassword"`
	MaxSessions      int    `json:"maxSessions"`
}

var (
	hostnamePattern         = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
	identifierPattern       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,62}$`)
	databasePasswordPattern = regexp.MustCompile(`^[A-Za-z0-9_.~!%+-]{12,128}$`)
)

func New(options Options) (*Server, error) {
	if len(options.Token) < 32 {
		return nil, errors.New("SETUP_TOKEN must contain at least 32 characters")
	}
	if options.Root == "" {
		return nil, errors.New("SETUP_ROOT is required")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, err
	}
	if options.Command == nil {
		return nil, errors.New("setup command runner is required")
	}
	state := "READY"
	message := "填写配置后开始安装。"
	if _, err := os.Stat(filepath.Join(root, ".setup-complete")); err == nil {
		state, message = "COMPLETE", "Cloud Browser 已完成初始化。"
	}
	return &Server{token: options.Token, root: root, dryRun: options.DryRun, command: options.Command, status: Status{State: state, Step: "准备", Message: message}}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(content, "ui")
	mux.Handle("GET /", http.FileServer(http.FS(assets)))
	mux.HandleFunc("GET /api/setup/status", s.getStatus)
	mux.HandleFunc("POST /api/setup/complete", s.complete)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return len(provided) == len(s.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "SETUP_TOKEN_INVALID")
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, s.status)
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "SETUP_TOKEN_INVALID")
		return
	}
	s.mu.RLock()
	currentState := s.status.State
	s.mu.RUnlock()
	if currentState == "RUNNING" {
		writeError(w, http.StatusConflict, "SETUP_RUNNING")
		return
	}
	if currentState == "COMPLETE" {
		writeError(w, http.StatusConflict, "SETUP_ALREADY_COMPLETE")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "JSON_REQUIRED")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var config Config
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	config, err := validate(config)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "INVALID_CONFIGURATION", "message": err.Error()})
		return
	}
	s.mu.Lock()
	if s.status.State != "READY" && s.status.State != "FAILED" {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "SETUP_STATE_CHANGED")
		return
	}
	s.status = Status{State: "RUNNING", Step: "保存配置", Message: "正在安全保存设置…", Progress: 5}
	s.mu.Unlock()
	go s.install(config)
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func validate(config Config) (Config, error) {
	config.Domain = strings.ToLower(strings.TrimSpace(config.Domain))
	config.AdminEmail = strings.ToLower(strings.TrimSpace(config.AdminEmail))
	config.DatabaseHost = strings.TrimSpace(config.DatabaseHost)
	config.DatabaseName = strings.TrimSpace(config.DatabaseName)
	config.DatabaseUser = strings.TrimSpace(config.DatabaseUser)
	if !hostnamePattern.MatchString(config.Domain) {
		return config, errors.New("请输入有效的完整域名，例如 browser.example.com")
	}
	if config.GatewayMode != "direct" && config.GatewayMode != "reverse-proxy" {
		return config, errors.New("请选择有效的 HTTPS 接入方式")
	}
	if config.DatabaseMode != "internal" && config.DatabaseMode != "external" {
		return config, errors.New("请选择内置或外部 PostgreSQL")
	}
	if !strings.Contains(config.AdminEmail, "@") || len(config.AdminEmail) > 254 {
		return config, errors.New("请输入有效的管理员邮箱")
	}
	if _, err := auth.Password(config.AdminPassword); err != nil {
		return config, errors.New("管理员密码需要 12–256 字节")
	}
	if config.MaxSessions < 1 || config.MaxSessions > 3 {
		return config, errors.New("首版并发会话数只能设为 1–3")
	}
	if !identifierPattern.MatchString(config.DatabaseName) || !identifierPattern.MatchString(config.DatabaseUser) {
		return config, errors.New("数据库名和用户只能包含字母、数字、下划线或连字符")
	}
	if !databasePasswordPattern.MatchString(config.DatabasePassword) {
		return config, errors.New("数据库密码需为 12–128 位，仅使用字母、数字和 . _ ~ ! % + -")
	}
	if config.DatabaseMode == "internal" {
		config.DatabaseHost, config.DatabasePort, config.DatabaseSSLMode = "postgres", 5432, "disable"
	} else {
		if net.ParseIP(config.DatabaseHost) == nil && !hostnamePattern.MatchString(config.DatabaseHost) && config.DatabaseHost != "localhost" {
			return config, errors.New("外部数据库主机无效")
		}
		if config.DatabasePort < 1 || config.DatabasePort > 65535 {
			return config, errors.New("数据库端口无效")
		}
		if config.DatabaseSSLMode != "require" && config.DatabaseSSLMode != "verify-full" && config.DatabaseSSLMode != "disable" {
			return config, errors.New("数据库 SSL 模式无效")
		}
	}
	return config, nil
}

func (s *Server) install(config Config) {
	fail := func(err error) {
		s.update("FAILED", "安装失败", err.Error(), 0, "")
	}
	dsn := databaseURL(config)
	if err := s.writeEnvironment(config, dsn); err != nil {
		fail(fmt.Errorf("保存配置失败：%w", err))
		return
	}
	if s.dryRun {
		s.update("COMPLETE", "安装完成", "演示模式配置检查通过。", 100, "https://"+config.Domain)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if config.DatabaseMode == "internal" {
		s.update("RUNNING", "启动数据库", "正在启动内置 PostgreSQL…", 15, "")
		if err := s.run(ctx, "docker", "compose", "up", "-d", "postgres"); err != nil {
			fail(fmt.Errorf("内置数据库启动失败：%w", err))
			return
		}
		// The setup container starts on its own published network. Joining the
		// Compose control network lets it validate the exact production DSN.
		_ = s.run(ctx, "docker", "network", "connect", "cloud-browser_control", env("SETUP_CONTAINER", "cloud-browser-setup"))
	} else {
		s.update("RUNNING", "连接数据库", "正在验证外部 PostgreSQL…", 15, "")
	}
	pool, err := waitForDatabase(ctx, dsn)
	if err != nil {
		fail(err)
		return
	}
	defer pool.Close()
	s.update("RUNNING", "初始化数据", "正在创建数据表和管理员…", 25, "")
	if _, err = pool.Exec(ctx, migrations.SQL); err != nil {
		fail(fmt.Errorf("数据库迁移失败：%w", err))
		return
	}
	if err = ensureAdministrator(ctx, pool, config.AdminEmail, config.AdminPassword); err != nil {
		fail(err)
		return
	}
	s.update("RUNNING", "构建浏览器", "首次构建 Chrome 镜像通常需要几分钟…", 40, "")
	if err = s.run(ctx, "docker", "build", "--platform", "linux/amd64", "-f", "deploy/browser/Dockerfile", "-t", "cloud-browser-browser:local", "."); err != nil {
		fail(fmt.Errorf("浏览器镜像构建失败：%w", err))
		return
	}
	s.update("RUNNING", "启动服务", "正在构建并启动网页与服务端…", 75, "")
	if err = s.run(ctx, "docker", "compose", "up", "-d", "--build"); err != nil {
		fail(fmt.Errorf("服务启动失败：%w", err))
		return
	}
	marker := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
	if err = os.WriteFile(filepath.Join(s.root, ".setup-complete"), marker, 0600); err != nil {
		fail(fmt.Errorf("写入完成标记失败：%w", err))
		return
	}
	s.update("COMPLETE", "安装完成", "Cloud Browser 已可使用。初始化服务将在稍后自动关闭。", 100, "https://"+config.Domain)
	_ = os.Remove("/etc/cloud-browser/setup-token")
	go func() {
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}()
}

func databaseURL(config Config) string {
	u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(config.DatabaseHost, strconv.Itoa(config.DatabasePort)), Path: "/" + config.DatabaseName}
	u.User = url.UserPassword(config.DatabaseUser, config.DatabasePassword)
	query := u.Query()
	query.Set("sslmode", config.DatabaseSSLMode)
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *Server) writeEnvironment(config Config, dsn string) error {
	lines := []string{
		"DOMAIN=" + config.Domain,
		"DATABASE_MODE=" + config.DatabaseMode,
		"DATABASE_URL=" + dsn,
		"POSTGRES_DB=" + config.DatabaseName,
		"POSTGRES_USER=" + config.DatabaseUser,
		"POSTGRES_PASSWORD=" + config.DatabasePassword,
		"MAX_SESSIONS=" + strconv.Itoa(config.MaxSessions),
	}
	if config.DatabaseMode == "internal" {
		lines = append(lines, "COMPOSE_PROFILES=database")
	}
	if config.GatewayMode == "reverse-proxy" {
		lines = append(lines, "GATEWAY_SITE=:80", "GATEWAY_HTTP_BIND=127.0.0.1:18088", "GATEWAY_HTTPS_BIND=127.0.0.1:18443")
	}
	temporary := filepath.Join(s.root, ".env.setup")
	if err := os.WriteFile(temporary, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(s.root, ".env"))
}

func waitForDatabase(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(90 * time.Second)
	for {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = pool.Ping(pingCtx)
			cancel()
			if err == nil {
				return pool, nil
			}
			pool.Close()
		}
		if time.Now().After(deadline) {
			return nil, errors.New("90 秒内无法连接 PostgreSQL，请检查地址、凭据和网络")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func ensureAdministrator(ctx context.Context, pool *pgxpool.Pool, email, password string) error {
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		var existingHash string
		var administrator bool
		err := pool.QueryRow(ctx, "SELECT password_hash,admin FROM users WHERE email=$1", email).Scan(&existingHash, &administrator)
		if err == nil && administrator && auth.Verify(existingHash, password) {
			return nil
		}
		return errors.New("数据库中已有账号，首次安装不会覆盖现有用户")
	}
	hash, err := auth.Password(password)
	if err != nil {
		return err
	}
	id := auth.ID()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "INSERT INTO users(id,email,password_hash,admin) VALUES($1,$2,$3,true)", id, email, hash); err == nil {
		_, err = tx.Exec(ctx, "INSERT INTO profiles(id,user_id) VALUES($1,$1)", id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, "INSERT INTO sessions(id,profile_id) VALUES($1,$1)", id)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Server) run(ctx context.Context, name string, arguments ...string) error {
	return s.command(withWorkingDirectory(ctx, s.root), name, arguments...)
}

type workingDirectoryKey struct{}

func withWorkingDirectory(ctx context.Context, directory string) context.Context {
	return context.WithValue(ctx, workingDirectoryKey{}, directory)
}

func CommandRunner(ctx context.Context, name string, arguments ...string) error {
	command := exec.CommandContext(ctx, name, arguments...)
	if directory, ok := ctx.Value(workingDirectoryKey{}).(string); ok {
		command.Dir = directory
	}
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func (s *Server) update(state, step, message string, progress int, origin string) {
	s.mu.Lock()
	s.status = Status{State: state, Step: step, Message: message, Progress: progress, Origin: origin}
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
