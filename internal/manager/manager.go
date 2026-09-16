package manager

import (
	"cloudbrowser/internal/auth"
	"cloudbrowser/internal/setup"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var (
	Version       = "dev"
	APIImage      = "ghcr.io/liyongliao/cloud-browser-api:latest"
	RunnerImage   = "ghcr.io/liyongliao/cloud-browser-runner:latest"
	GatewayImage  = "ghcr.io/liyongliao/cloud-browser-gateway:latest"
	BrowserImage  = "ghcr.io/liyongliao/cloud-browser-browser:latest"
	PostgresImage = "postgres:17-bookworm"
)

const (
	defaultRoot = "/var/lib/cloud-browser"
	legacyRoot  = "/opt/cloud-browser"
	listenAddr  = "127.0.0.1:8090"
)

type Installation struct {
	Kind         string `json:"kind"`
	Root         string `json:"root"`
	DatabaseMode string `json:"databaseMode"`
	Version      string `json:"version"`
}

func Run(args []string) int {
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	if command == "version" || command == "--version" || command == "-v" {
		fmt.Println("Cloud Browser", Version)
		return 0
	}
	if command == "help" || command == "--help" || command == "-h" {
		usage()
		return 0
	}
	if command == "_setup-server" {
		if err := serveSetup(); err != nil {
			fmt.Fprintln(os.Stderr, "安装向导启动失败：", err)
			return 1
		}
		return 0
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "请使用 sudo 运行 Cloud Browser 管理程序。")
		return 1
	}
	if command == "" {
		if _, err := findInstallation(true); err == nil {
			command = "status"
		} else {
			command = "setup"
		}
	}
	var err error
	switch command {
	case "setup":
		err = launchSetup()
	case "start":
		err = lifecycle("start")
	case "stop":
		err = stop()
	case "status":
		err = status()
	case "logs":
		err = logs()
	case "doctor":
		err = doctor()
	default:
		usage()
		fmt.Fprintln(os.Stderr, "未知命令：", command)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Println(`Cloud Browser 单文件安装与管理程序

用法：cloud-browser [setup|start|stop|status|logs|doctor|version]

无参数运行时，未安装则启动网页向导，已安装则显示状态。`)
}

func rootDir() string {
	if value := os.Getenv("CLOUD_BROWSER_ROOT"); value != "" {
		return value
	}
	return defaultRoot
}

func launchSetup() error {
	if installation, err := findInstallation(true); err == nil {
		fmt.Printf("检测到现有 Cloud Browser 部署（%s），未创建任何新数据库。\n", installation.Root)
		return showStatus(installation)
	}
	if err := validatePlatform(); err != nil {
		return err
	}
	root := rootDir()
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if activeSetup() {
		return printSetupLink(root)
	}
	token := auth.Token()
	if err := os.WriteFile(filepath.Join(root, "setup-token"), []byte(token+"\n"), 0600); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(root, "setup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "_setup-server")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(os.Environ(), "CLOUD_BROWSER_ROOT="+root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if activeSetup() {
			return printSetupLink(root)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("安装向导未能启动，请查看 %s", filepath.Join(root, "setup.log"))
}

func activeSetup() bool {
	connection, err := net.DialTimeout("tcp", listenAddr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}

func printSetupLink(root string) error {
	token, err := os.ReadFile(filepath.Join(root, "setup-token"))
	if err != nil {
		return err
	}
	fmt.Println("Cloud Browser 安装向导已在后台运行。")
	fmt.Println("在自己的电脑执行：ssh -L 8090:127.0.0.1:8090 root@服务器地址")
	fmt.Printf("然后打开：http://127.0.0.1:8090/#token=%s\n", strings.TrimSpace(string(token)))
	fmt.Printf("安装日志：%s\n", filepath.Join(root, "setup.log"))
	return nil
}

func serveSetup() error {
	if err := validatePlatform(); err != nil {
		return err
	}
	root := rootDir()
	lock, err := os.OpenFile(filepath.Join(root, "setup.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("另一个安装向导正在运行")
	}
	tokenBytes, err := os.ReadFile(filepath.Join(root, "setup-token"))
	if err != nil {
		return err
	}
	installer := NewInstaller(root)
	server, err := setup.New(setup.Options{
		Token: strings.TrimSpace(string(tokenBytes)), Root: root,
		Install: installer.Install, TestDatabase: installer.TestDatabase,
		LocalDatabaseDetected: localPostgresDetected(),
	})
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: listenAddr, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	stopMonitor := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopMonitor:
				return
			case <-ticker.C:
				if _, markerErr := os.Stat(filepath.Join(root, ".setup-complete")); markerErr == nil {
					timer := time.NewTimer(10 * time.Minute)
					select {
					case <-stopMonitor:
						timer.Stop()
						return
					case <-timer.C:
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						_ = httpServer.Shutdown(ctx)
						cancel()
						return
					}
				}
			}
		}
	}()
	err = httpServer.ListenAndServe()
	close(stopMonitor)
	if err == http.ErrServerClosed {
		err = nil
	}
	return err
}

func localPostgresDetected() bool {
	connection, err := net.DialTimeout("tcp", "127.0.0.1:5432", 300*time.Millisecond)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}

func validatePlatform() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("首版仅支持 x86_64 Ubuntu 24.04 与 Debian 13")
	}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return errors.New("无法识别 Linux 发行版")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			values[key] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	if (values["ID"] != "ubuntu" || values["VERSION_ID"] != "24.04") && (values["ID"] != "debian" || values["VERSION_ID"] != "13") {
		return fmt.Errorf("检测到 %s %s；自动安装仅支持 Ubuntu 24.04 与 Debian 13", values["ID"], values["VERSION_ID"])
	}
	return nil
}

func findInstallation(persistLegacy bool) (Installation, error) {
	root := rootDir()
	statePath := filepath.Join(root, "installation.json")
	if data, err := os.ReadFile(statePath); err == nil {
		var installation Installation
		if json.Unmarshal(data, &installation) == nil && installation.Root != "" {
			return installation, nil
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".setup-complete")); err == nil {
		mode := envValue(filepath.Join(root, ".env"), "DATABASE_MODE")
		return Installation{Kind: "managed", Root: root, DatabaseMode: mode, Version: Version}, nil
	}
	_, legacyEnvErr := os.Stat(filepath.Join(legacyRoot, ".env"))
	_, legacyComposeErr := os.Stat(filepath.Join(legacyRoot, "compose.yaml"))
	if legacyEnvErr == nil && legacyComposeErr == nil {
		installation := Installation{Kind: "legacy", Root: legacyRoot, DatabaseMode: envValue(filepath.Join(legacyRoot, ".env"), "DATABASE_MODE"), Version: "source"}
		if persistLegacy {
			var err error
			if err = os.MkdirAll(root, 0700); err == nil {
				err = writeJSONFile(filepath.Join(root, "installation.json"), installation, 0600)
			}
			if err != nil {
				return Installation{}, fmt.Errorf("记录现有部署失败：%w", err)
			}
		}
		return installation, nil
	}
	return Installation{}, errors.New("尚未安装 Cloud Browser，请运行 cloud-browser setup")
}

func status() error {
	installation, err := findInstallation(true)
	if err != nil {
		return err
	}
	return showStatus(installation)
}

func showStatus(installation Installation) error {
	domain := envValue(filepath.Join(installation.Root, ".env"), "DOMAIN")
	fmt.Printf("Cloud Browser %s\n部署目录：%s\n数据库来源：%s\n", Version, installation.Root, databaseLabel(installation.DatabaseMode))
	if domain != "" {
		fmt.Println("访问地址：https://" + domain)
	}
	return runCommand(installation.Root, "docker", composeArgs(installation, "ps")...)
}

func lifecycle(action string) error {
	installation, err := findInstallation(true)
	if err != nil {
		return err
	}
	return runCommand(installation.Root, "docker", composeArgs(installation, action)...)
}

func stop() error {
	installation, err := findInstallation(true)
	if err != nil {
		return err
	}
	ids, _ := exec.Command("docker", "ps", "-q", "--filter", "label=cloud-browser.managed=true").Output()
	for _, id := range strings.Fields(string(ids)) {
		if err = runCommand("", "docker", "stop", "--time", "30", id); err != nil {
			return err
		}
	}
	return runCommand(installation.Root, "docker", composeArgs(installation, "stop")...)
}

func logs() error {
	installation, err := findInstallation(true)
	if err != nil {
		return err
	}
	return runCommand(installation.Root, "docker", composeArgs(installation, "logs", "--tail=200", "-f")...)
}

func doctor() error {
	if err := validatePlatform(); err != nil {
		return err
	}
	checks := []string{"docker", "apparmor_parser", "iptables", "ip6tables"}
	failed := false
	for _, name := range checks {
		if _, err := exec.LookPath(name); err != nil {
			fmt.Println("✗", name, "未安装")
			failed = true
		} else {
			fmt.Println("✓", name)
		}
	}
	if output, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		fmt.Println("✗ Docker Compose 不可用：", strings.TrimSpace(string(output)))
		failed = true
	} else {
		fmt.Println("✓ Docker Compose")
	}
	if _, err := findInstallation(false); err == nil {
		fmt.Println("✓ 已识别 Cloud Browser 部署")
	} else {
		fmt.Println("· 尚未安装 Cloud Browser")
	}
	if failed {
		return errors.New("环境检查未通过")
	}
	return nil
}

func composeArgs(installation Installation, tail ...string) []string {
	args := []string{"compose", "--project-directory", installation.Root, "-f", filepath.Join(installation.Root, "compose.yaml")}
	if installation.DatabaseMode == "internal" {
		args = append(args, "-f", filepath.Join(installation.Root, "compose.database.yaml"))
	}
	return append(args, tail...)
}

func databaseLabel(mode string) string {
	switch mode {
	case "internal":
		return "内置 PostgreSQL"
	case "local":
		return "服务器本机已有 PostgreSQL"
	case "remote", "external":
		return "远程 PostgreSQL"
	default:
		return "沿用现有配置"
	}
}

func envValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func writeJSONFile(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err = os.WriteFile(temporary, data, mode); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func runCommand(directory, name string, args ...string) error {
	command := exec.Command(name, args...)
	if directory != "" {
		command.Dir = directory
	}
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s 执行失败：%w", name, err)
	}
	return nil
}
