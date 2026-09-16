package manager

import (
	browserassets "cloudbrowser/deploy/browser"
	"cloudbrowser/internal/auth"
	"cloudbrowser/internal/setup"
	scriptassets "cloudbrowser/scripts"
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/*
var managerAssets embed.FS

type Installer struct {
	Root string
}

func NewInstaller(root string) *Installer { return &Installer{Root: root} }

func (i *Installer) Install(ctx context.Context, config setup.Config, update setup.UpdateFunc) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	step := func(name, message string, progress int) {
		update(setup.Status{State: "RUNNING", Step: name, Message: message, Progress: progress})
	}
	step("检查服务器", "正在准备 Docker 与主机隔离…", 8)
	if err := i.ensureDocker(ctx); err != nil {
		return err
	}
	if err := i.writeAssets(); err != nil {
		return fmt.Errorf("写入运行文件失败：%w", err)
	}
	if err := i.prepareHost(ctx); err != nil {
		return err
	}
	if err := i.ensureControlNetwork(ctx); err != nil {
		return err
	}
	if config.DatabaseMode != "internal" && commandOK(ctx, "docker", "inspect", "cloud-browser-postgres-1") {
		return errors.New("本次安装已经创建内置 PostgreSQL；为避免遗留或重复数据库，请继续选择内置数据库")
	}

	if config.DatabaseMode == "internal" {
		password, err := i.internalDatabasePassword()
		if err != nil {
			return err
		}
		config.DatabaseHost, config.DatabasePort = "postgres", 5432
		config.DatabaseName, config.DatabaseUser = "cloudbrowser", "cloudbrowser"
		config.DatabasePassword, config.DatabaseSSLMode = password, "disable"
	}
	if err := i.writeEnvironment(config); err != nil {
		return fmt.Errorf("保存配置失败：%w", err)
	}

	if config.DatabaseMode == "internal" {
		step("创建内置数据库", "正在下载并启动项目专用 PostgreSQL…", 24)
		if err := i.pull(ctx, image("CLOUD_BROWSER_POSTGRES_IMAGE", PostgresImage)); err != nil {
			return fmt.Errorf("PostgreSQL 镜像下载失败：%w", err)
		}
		if err := i.compose(ctx, config.DatabaseMode, "up", "-d", "--no-build", "postgres"); err != nil {
			return fmt.Errorf("内置 PostgreSQL 启动失败：%w", err)
		}
	} else {
		step("验证已有数据库", "正在从应用容器网络检查连接和建表权限…", 24)
	}

	step("验证数据库", "正在从应用容器网络检查连接和建表权限…", 36)
	apiImage := image("CLOUD_BROWSER_API_IMAGE", APIImage)
	if err := i.pull(ctx, apiImage); err != nil {
		return fmt.Errorf("连接检查镜像下载失败（%s）：%w", apiImage, err)
	}
	if err := i.testDatabaseRetry(ctx, config, config.DatabaseMode == "internal"); err != nil {
		if config.DatabaseMode == "local" {
			return fmt.Errorf("无法从应用容器连接本机 PostgreSQL。请确认数据库监听 Docker 网桥地址，并允许 Docker 网段访问：%w", err)
		}
		return err
	}
	step("下载服务", "数据库可用，正在下载其余预构建镜像…", 46)
	for _, reference := range []string{
		image("CLOUD_BROWSER_RUNNER_IMAGE", RunnerImage),
		image("CLOUD_BROWSER_GATEWAY_IMAGE", GatewayImage),
		image("CLOUD_BROWSER_BROWSER_IMAGE", BrowserImage),
	} {
		if err := i.pull(ctx, reference); err != nil {
			return fmt.Errorf("镜像下载失败（%s）：%w", reference, err)
		}
	}

	step("初始化账号", "正在创建数据表和首个管理员…", 62)
	if err := i.createAdministrator(ctx, config); err != nil {
		return err
	}
	step("启动服务", "正在启动 API、Runner 与网页网关…", 76)
	if err := i.compose(ctx, config.DatabaseMode, "up", "-d", "--no-build"); err != nil {
		return fmt.Errorf("服务启动失败：%w", err)
	}
	step("验证服务", "正在等待正式入口通过健康检查…", 90)
	if err := i.waitForHealth(ctx, config); err != nil {
		return err
	}
	installation := Installation{Kind: "managed", Root: i.Root, DatabaseMode: config.DatabaseMode, Version: Version}
	if err := writeJSONFile(filepath.Join(i.Root, "installation.json"), installation, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(i.Root, ".setup-complete"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(i.Root, ".database-password"))
	_ = os.Remove(filepath.Join(i.Root, "setup-token"))
	update(setup.Status{State: "COMPLETE", Step: "安装完成", Message: "Cloud Browser 已可使用。", Progress: 100, Origin: "https://" + config.Domain})
	return nil
}

func (i *Installer) TestDatabase(ctx context.Context, config setup.Config) error {
	if config.DatabaseMode == "internal" {
		return nil
	}
	if err := i.ensureDocker(ctx); err != nil {
		return err
	}
	if err := i.writeAssets(); err != nil {
		return err
	}
	if err := i.ensureControlNetwork(ctx); err != nil {
		return err
	}
	if err := i.pull(ctx, image("CLOUD_BROWSER_API_IMAGE", APIImage)); err != nil {
		return fmt.Errorf("连接检查镜像下载失败：%w", err)
	}
	return i.testDatabaseOnce(ctx, config)
}

func (i *Installer) ensureDocker(ctx context.Context) error {
	if err := validatePlatform(); err != nil {
		return err
	}
	dockerCommand := executableExists("docker")
	composeAvailable := dockerCommand && commandOK(ctx, "docker", "compose", "version")
	hostToolsAvailable := executableExists("apparmor_parser") && executableExists("iptables") && executableExists("ip6tables")
	if dockerCommand && composeAvailable && hostToolsAvailable && commandOK(ctx, "docker", "info") {
		return nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return errors.New("系统依赖不完整；自动安装仅支持 apt 系统")
	}
	environment := append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	packages := []string{}
	if !dockerCommand {
		packages = append(packages, "docker.io")
	}
	if !hostToolsAvailable {
		packages = append(packages, "apparmor", "apparmor-utils", "iptables", "ca-certificates")
	}
	if len(packages) > 0 {
		if err := runWithEnv(ctx, environment, "apt-get", "update", "-qq"); err != nil {
			return fmt.Errorf("更新软件索引失败：%w", err)
		}
		if err := runWithEnv(ctx, environment, "apt-get", append([]string{"install", "-y"}, packages...)...); err != nil {
			return fmt.Errorf("系统依赖安装失败：%w", err)
		}
	}
	if !composeAvailable {
		if len(packages) == 0 {
			if err := runWithEnv(ctx, environment, "apt-get", "update", "-qq"); err != nil {
				return fmt.Errorf("更新软件索引失败：%w", err)
			}
		}
		composeInstalled := runWithEnv(ctx, environment, "apt-get", "install", "-y", "docker-compose-v2") == nil
		if !composeInstalled {
			composeInstalled = runWithEnv(ctx, environment, "apt-get", "install", "-y", "docker-compose-plugin") == nil
		}
		if !composeInstalled {
			return errors.New("无法安装 Docker Compose v2")
		}
	}
	if err := runCommand("", "systemctl", "enable", "--now", "docker"); err != nil {
		return err
	}
	if !commandOK(ctx, "docker", "info") || !commandOK(ctx, "docker", "compose", "version") || !executableExists("apparmor_parser") || !executableExists("iptables") || !executableExists("ip6tables") {
		return errors.New("Docker、Compose 或主机隔离依赖安装后仍不可用")
	}
	return nil
}

func executableExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (i *Installer) writeAssets() error {
	if err := os.MkdirAll(i.Root, 0700); err != nil {
		return err
	}
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{filepath.Join(i.Root, "compose.yaml"), mustRead(managerAssets.ReadFile("assets/compose.yaml")), 0600},
		{filepath.Join(i.Root, "compose.database.yaml"), mustRead(managerAssets.ReadFile("assets/compose.database.yaml")), 0600},
	}
	for _, file := range files {
		if err := writeAtomic(file.path, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

func (i *Installer) prepareHost(ctx context.Context) error {
	data, err := os.ReadFile("/proc/sys/user/max_user_namespaces")
	if err != nil || strings.TrimSpace(string(data)) == "0" {
		return errors.New("内核未启用用户命名空间，无法安全运行 Chrome 沙箱")
	}
	if err = os.MkdirAll("/srv/cloud-browser/profiles", 0700); err != nil {
		return err
	}
	if err = os.MkdirAll("/etc/cloud-browser", 0755); err != nil {
		return err
	}
	if err = os.MkdirAll("/etc/apparmor.d", 0755); err != nil {
		return err
	}
	apparmor, err := browserassets.Files.ReadFile("apparmor.profile")
	if err != nil {
		return err
	}
	seccomp, err := browserassets.Files.ReadFile("chrome-seccomp.json")
	if err != nil {
		return err
	}
	firewall, err := scriptassets.Files.ReadFile("firewall.sh")
	if err != nil {
		return err
	}
	unit, err := managerAssets.ReadFile("assets/cloud-browser-firewall.service")
	if err != nil {
		return err
	}
	for _, file := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{"/etc/apparmor.d/cloud-browser", apparmor, 0644},
		{"/etc/cloud-browser/chrome-seccomp.json", seccomp, 0644},
		{"/etc/cloud-browser/firewall.sh", firewall, 0755},
		{"/etc/systemd/system/cloud-browser-firewall.service", unit, 0644},
	} {
		if err = writeAtomic(file.path, file.data, file.mode); err != nil {
			return err
		}
	}
	if err = runCommand("", "apparmor_parser", "-r", "/etc/apparmor.d/cloud-browser"); err != nil {
		return fmt.Errorf("加载 AppArmor 失败：%w", err)
	}
	if err = runCommand("", "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err = runCommand("", "systemctl", "enable", "--now", "cloud-browser-firewall.service"); err != nil {
		return fmt.Errorf("启用网络隔离失败：%w", err)
	}
	return nil
}

func (i *Installer) ensureControlNetwork(ctx context.Context) error {
	if commandOK(ctx, "docker", "network", "inspect", "cloud-browser-control") {
		return nil
	}
	return runCommand("", "docker", "network", "create", "--driver", "bridge", "cloud-browser-control")
}

func (i *Installer) internalDatabasePassword() (string, error) {
	if password := envValue(filepath.Join(i.Root, ".env"), "POSTGRES_PASSWORD"); password != "" {
		return password, nil
	}
	path := filepath.Join(i.Root, ".database-password")
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data)), nil
	}
	password := auth.Token()
	if err := os.WriteFile(path, []byte(password+"\n"), 0600); err != nil {
		return "", err
	}
	return password, nil
}

func (i *Installer) writeEnvironment(config setup.Config) error {
	values := []string{
		"DOMAIN=" + config.Domain,
		"DATABASE_MODE=" + config.DatabaseMode,
		"DATABASE_URL=" + databaseURL(config),
		"MAX_SESSIONS=" + strconv.Itoa(config.MaxSessions),
		"API_IMAGE=" + image("CLOUD_BROWSER_API_IMAGE", APIImage),
		"RUNNER_IMAGE=" + image("CLOUD_BROWSER_RUNNER_IMAGE", RunnerImage),
		"GATEWAY_IMAGE=" + image("CLOUD_BROWSER_GATEWAY_IMAGE", GatewayImage),
		"BROWSER_IMAGE=" + image("CLOUD_BROWSER_BROWSER_IMAGE", BrowserImage),
		"POSTGRES_IMAGE=" + image("CLOUD_BROWSER_POSTGRES_IMAGE", PostgresImage),
	}
	if config.DatabaseMode == "internal" {
		values = append(values,
			"POSTGRES_DB="+config.DatabaseName,
			"POSTGRES_USER="+config.DatabaseUser,
			"POSTGRES_PASSWORD="+config.DatabasePassword,
		)
	}
	if config.GatewayMode == "reverse-proxy" {
		values = append(values, "GATEWAY_SITE=:80", "GATEWAY_HTTP_BIND=127.0.0.1:18088", "GATEWAY_HTTPS_BIND=127.0.0.1:18443")
	}
	return writeAtomic(filepath.Join(i.Root, ".env"), []byte(strings.Join(values, "\n")+"\n"), 0600)
}

func databaseURL(config setup.Config) string {
	address := net.JoinHostPort(config.DatabaseHost, strconv.Itoa(config.DatabasePort))
	u := &url.URL{Scheme: "postgres", Host: address, Path: "/" + config.DatabaseName, User: url.UserPassword(config.DatabaseUser, config.DatabasePassword)}
	query := u.Query()
	query.Set("sslmode", config.DatabaseSSLMode)
	u.RawQuery = query.Encode()
	return u.String()
}

func (i *Installer) testDatabaseRetry(ctx context.Context, config setup.Config, retry bool) error {
	deadline := time.Now().Add(90 * time.Second)
	for {
		err := i.testDatabaseOnce(ctx, config)
		if err == nil {
			return nil
		}
		if !retry || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (i *Installer) testDatabaseOnce(ctx context.Context, config setup.Config) error {
	environmentFile, err := os.CreateTemp(i.Root, ".database-test-")
	if err != nil {
		return err
	}
	environmentPath := environmentFile.Name()
	defer os.Remove(environmentPath)
	if err = environmentFile.Chmod(0600); err == nil {
		_, err = environmentFile.WriteString("DATABASE_URL=" + databaseURL(config) + "\n")
	}
	closeErr := environmentFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	args := []string{"run", "--rm", "--network", "cloud-browser-control", "--add-host", "host.docker.internal:host-gateway", "--entrypoint", "dbcheck", "--env-file", environmentPath, image("CLOUD_BROWSER_API_IMAGE", APIImage)}
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("PostgreSQL 连接或建表权限检查失败：%s", message)
	}
	return nil
}

func (i *Installer) createAdministrator(ctx context.Context, config setup.Config) error {
	passwordFile, err := os.CreateTemp(i.Root, ".admin-password-")
	if err != nil {
		return err
	}
	passwordPath := passwordFile.Name()
	defer os.Remove(passwordPath)
	// The API image runs as uid 10001. Keep the temporary secret inside the
	// root-only installation directory, while allowing that container user to
	// read only this bind-mounted file.
	if err = passwordFile.Chown(10001, 10001); err == nil {
		err = passwordFile.Chmod(0400)
	}
	if err == nil {
		_, err = passwordFile.WriteString(config.AdminPassword)
	}
	closeErr := passwordFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	args := []string{
		"run", "--rm", "--network", "cloud-browser-control",
		"--add-host", "host.docker.internal:host-gateway",
		"--entrypoint", "admin",
		"--env-file", filepath.Join(i.Root, ".env"),
		"-e", "ADMIN_PASSWORD_FILE=/run/secrets/admin-password",
		"-v", passwordPath + ":/run/secrets/admin-password:ro",
		image("CLOUD_BROWSER_API_IMAGE", APIImage), "create", config.AdminEmail,
	}
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("管理员初始化失败：%s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (i *Installer) compose(ctx context.Context, mode string, tail ...string) error {
	installation := Installation{Kind: "managed", Root: i.Root, DatabaseMode: mode}
	command := exec.CommandContext(ctx, "docker", composeArgs(installation, tail...)...)
	command.Dir, command.Stdout, command.Stderr = i.Root, os.Stdout, os.Stderr
	return command.Run()
}

func (i *Installer) pull(ctx context.Context, reference string) error {
	command := exec.CommandContext(ctx, "docker", "pull", reference)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func (i *Installer) waitForHealth(ctx context.Context, config setup.Config) error {
	address := "https://" + config.Domain + "/healthz"
	client := &http.Client{Timeout: 8 * time.Second}
	if config.GatewayMode == "reverse-proxy" {
		address = "http://127.0.0.1:18088/healthz"
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("服务已启动，但正式入口 %s 未在两分钟内通过健康检查", address)
}

func image(environment, fallback string) string {
	if value := os.Getenv(environment); value != "" {
		return value
	}
	return fallback
}

func commandOK(ctx context.Context, name string, args ...string) bool {
	return exec.CommandContext(ctx, name, args...).Run() == nil
}

func runWithEnv(ctx context.Context, environment []string, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = environment
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	temporary := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(temporary, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(temporary, mode); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func mustRead(data []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return data
}
