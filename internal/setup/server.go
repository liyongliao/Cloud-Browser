package setup

import (
	"cloudbrowser/internal/auth"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed ui/*
var content embed.FS

type UpdateFunc func(Status)
type InstallFunc func(context.Context, Config, UpdateFunc) error
type TestDatabaseFunc func(context.Context, Config) error

type Options struct {
	Token                 string
	Root                  string
	DryRun                bool
	Install               InstallFunc
	TestDatabase          TestDatabaseFunc
	LocalDatabaseDetected bool
}

type Server struct {
	token                 string
	root                  string
	dryRun                bool
	install               InstallFunc
	testDatabase          TestDatabaseFunc
	localDatabaseDetected bool
	mu                    sync.RWMutex
	status                Status
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
	hostnamePattern   = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,62}$`)
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
	if !options.DryRun && options.Install == nil {
		return nil, errors.New("setup installer is required")
	}
	state, message := "READY", "选择数据库来源后开始安装。"
	if _, err := os.Stat(filepath.Join(root, ".setup-complete")); err == nil {
		state, message = "COMPLETE", "Cloud Browser 已完成初始化。"
	}
	return &Server{
		token: options.Token, root: root, dryRun: options.DryRun,
		install: options.Install, testDatabase: options.TestDatabase,
		localDatabaseDetected: options.LocalDatabaseDetected,
		status:                Status{State: state, Step: "准备", Message: message},
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(content, "ui")
	mux.Handle("GET /", http.FileServer(http.FS(assets)))
	mux.HandleFunc("GET /api/setup/status", s.getStatus)
	mux.HandleFunc("GET /api/setup/environment", s.getEnvironment)
	mux.HandleFunc("POST /api/setup/database/test", s.testConnection)
	mux.HandleFunc("POST /api/setup/complete", s.complete)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) getEnvironment(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "SETUP_TOKEN_INVALID")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"localPostgresDetected": s.localDatabaseDetected})
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

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "SETUP_TOKEN_INVALID")
		return
	}
	config, ok := decodeConfig(w, r)
	if !ok {
		return
	}
	var err error
	config, err = validateDatabase(config, false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "INVALID_DATABASE_CONFIGURATION", "message": err.Error()})
		return
	}
	if config.DatabaseMode == "internal" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "内置数据库将在确认安装后创建。"})
		return
	}
	if s.testDatabase == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "DATABASE_TEST_UNAVAILABLE", "message": "当前环境无法执行容器网络连接测试。"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := s.testDatabase(ctx, config); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "DATABASE_CONNECTION_FAILED", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "连接、认证和建表权限检查通过。"})
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
	config, ok := decodeConfig(w, r)
	if !ok {
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
	s.status = Status{State: "RUNNING", Step: "准备安装", Message: "正在检查服务器环境…", Progress: 5}
	s.mu.Unlock()
	go s.runInstall(config)
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func decodeConfig(w http.ResponseWriter, r *http.Request) (Config, bool) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "JSON_REQUIRED")
		return Config{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var config Config
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return Config{}, false
	}
	return config, true
}

func validate(config Config) (Config, error) {
	config.Domain = strings.ToLower(strings.TrimSpace(config.Domain))
	config.AdminEmail = strings.ToLower(strings.TrimSpace(config.AdminEmail))
	if !hostnamePattern.MatchString(config.Domain) {
		return config, errors.New("请输入有效的完整域名，例如 browser.example.com")
	}
	if config.GatewayMode != "direct" && config.GatewayMode != "reverse-proxy" {
		return config, errors.New("请选择有效的 HTTPS 接入方式")
	}
	if !strings.Contains(config.AdminEmail, "@") || len(config.AdminEmail) > 254 {
		return config, errors.New("请输入有效的管理员邮箱")
	}
	if _, err := auth.Password(config.AdminPassword); err != nil {
		return config, errors.New("管理员密码需要 12–256 字节")
	}
	if config.MaxSessions < 1 || config.MaxSessions > 3 {
		return config, errors.New("并发会话数只能设为 1–3")
	}
	return validateDatabase(config, true)
}

func validateDatabase(config Config, allowInternal bool) (Config, error) {
	config.DatabaseHost = strings.TrimSpace(config.DatabaseHost)
	config.DatabaseName = strings.TrimSpace(config.DatabaseName)
	config.DatabaseUser = strings.TrimSpace(config.DatabaseUser)
	if config.DatabaseMode != "internal" && config.DatabaseMode != "local" && config.DatabaseMode != "remote" {
		return config, errors.New("请选择内置、本机已有或远程 PostgreSQL")
	}
	if config.DatabaseMode == "internal" {
		if !allowInternal {
			return config, nil
		}
		config.DatabaseHost = "postgres"
		config.DatabasePort = 5432
		config.DatabaseName = "cloudbrowser"
		config.DatabaseUser = "cloudbrowser"
		config.DatabasePassword = ""
		config.DatabaseSSLMode = "disable"
		return config, nil
	}
	if !identifierPattern.MatchString(config.DatabaseName) || !identifierPattern.MatchString(config.DatabaseUser) {
		return config, errors.New("数据库名和用户只能包含字母、数字、下划线或连字符")
	}
	if config.DatabasePort < 1 || config.DatabasePort > 65535 {
		return config, errors.New("数据库端口无效")
	}
	if config.DatabasePassword == "" || len(config.DatabasePassword) > 256 || strings.ContainsAny(config.DatabasePassword, "\r\n") {
		return config, errors.New("请输入数据库密码，最长 256 字节且不能包含换行")
	}
	if config.DatabaseSSLMode != "require" && config.DatabaseSSLMode != "verify-full" && config.DatabaseSSLMode != "disable" {
		return config, errors.New("数据库 SSL 模式无效")
	}
	if config.DatabaseMode == "local" {
		config.DatabaseHost = "host.docker.internal"
		return config, nil
	}
	if net.ParseIP(config.DatabaseHost) == nil && !hostnamePattern.MatchString(config.DatabaseHost) {
		return config, errors.New("远程数据库主机无效")
	}
	return config, nil
}

func (s *Server) runInstall(config Config) {
	if s.dryRun {
		s.update(Status{State: "COMPLETE", Step: "安装完成", Message: "演示模式配置检查通过。", Progress: 100, Origin: "https://" + config.Domain})
		return
	}
	if err := s.install(context.Background(), config, s.update); err != nil {
		s.update(Status{State: "FAILED", Step: "安装失败", Message: err.Error()})
	}
}

func (s *Server) update(status Status) {
	s.mu.Lock()
	s.status = status
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
