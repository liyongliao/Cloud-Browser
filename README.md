# Cloud Browser

把一台服务器变成属于账号的随身 Chrome。用户从电脑、平板或手机登录网页，就能继续使用同一份标签页、Cookie、收藏夹、历史记录、网站存储和文件；Chrome/Edge 插件可以把当前网址一键送到云端打开。

![Cloud Browser 登录页](docs/images/product-login.png)

## 它能做什么

- 每个账号拥有独立的真实 Chrome 容器和持久化 Profile。
- 网页内提供画面、鼠标键盘、中文输入、文本剪贴板、声音、上传与下载。
- 新设备可以主动接管控制权，旧设备立即失去输入和画面连接。
- 所有设备断开 10 分钟后自动关闭 Chrome，下次访问恢复浏览数据。
- 管理员可邀请或禁用用户、查看资源、停止异常会话；每位用户都能自行修改密码。
- 浏览器以非 root 身份运行，保持 Chrome 沙箱，禁止访问宿主控制面、私网和云元数据地址。

产品定位是个人与少量受邀用户的单机版本，默认最多 3 个同时运行的浏览器。4 GB 内存服务器建议设为 1 个会话，3 个会话建议至少 4 核、8 GB 内存和 SSD。

## 首次安装

准备一台带公网 IP 的 x86_64 Ubuntu 24.04 或 Debian 13 服务器，并把域名 A 记录指向该服务器。下载并运行单个管理程序：

```bash
curl -fLO https://github.com/liyongliao/Cloud-Browser/releases/latest/download/cloud-browser-linux-amd64
curl -fLO https://github.com/liyongliao/Cloud-Browser/releases/latest/download/cloud-browser-linux-amd64.sha256
sha256sum -c cloud-browser-linux-amd64.sha256
chmod +x cloud-browser-linux-amd64
sudo ./cloud-browser-linux-amd64
```

程序在后台启动仅监听本机的一次性向导，并显示 SSH 转发命令。网页先让你选择数据库来源，再设置域名、管理员账号和并发容量。程序会复用现有 Docker；缺少 Docker 时只在提交安装或测试已有数据库连接后安装。

![网页选择 PostgreSQL 来源](docs/images/setup-database.png)

数据库支持三种方式：

- 内置 PostgreSQL：确认安装后才下载镜像，只创建一个项目专用服务和数据卷，失败重试会复用它。
- 本机已有 PostgreSQL：通过 Docker 宿主网关连接当前 Linux 服务器的实例，不创建数据库服务。
- 远程 PostgreSQL：连接云数据库或另一台服务器，不创建本机数据库服务。

已有数据库可在向导中从应用实际容器网络测试连接、认证与建表权限。连接失败不会切换成内置数据库。管理员密码只用于创建账号，不写入部署配置或安装日志；初始化完成后入口失效。

![安装前确认](docs/images/setup-summary.png)

完整图文步骤、已有 Nginx 的接入方式和故障处理见 [首次安装教程](docs/installation-guide.md)。

安装后的日常管理统一使用：

```bash
sudo cloud-browser status
sudo cloud-browser start
sudo cloud-browser stop
sudo cloud-browser logs
sudo cloud-browser doctor
```

## 日常使用

登录后从“我的浏览器”启动或接管云端 Chrome；“我的文件”负责本地与云端文件往返；“账号设置”可以修改自己的密码，保存后其他设备需要重新登录。

![用户修改密码](docs/images/account-password.png)

详细操作见 [使用教程](docs/user-guide.md)，部署、备份、恢复和升级见 [部署与运维](docs/deployment.md)。

## 技术结构

```mermaid
flowchart LR
    E[Chrome / Edge 插件] --> W[响应式 Web]
    M[电脑 / 手机浏览器] --> W
    W --> G[HTTPS 网关]
    G --> A[Go API]
    A --> P[(PostgreSQL)]
    A --> R[受限 Runner]
    R --> C[每用户 Chrome + KasmVNC]
    C --> D[(持久化 Profile 与文件)]
```

| 模块                                 | 实现                                                    |
| ------------------------------------ | ------------------------------------------------------- |
| Web 与插件                           | React、TypeScript、Vite、Manifest V3                    |
| API、Runner、Browser Agent、安装向导 | Go                                                      |
| 数据库                               | 内置 PostgreSQL 17、本机已有或远程 PostgreSQL           |
| 浏览器                               | Ubuntu 24.04、Google Chrome Stable、KasmVNC、PulseAudio |
| 网关与部署                           | Caddy、Docker Compose；可接入现有 Nginx                 |

管理程序是静态 Linux 可执行文件，内嵌运行模板和隔离配置；正式发布版通过镜像摘要固定 API、Runner、网页网关和浏览器镜像。服务器不需要 Go、Node.js、Git 或本地镜像构建。

业务接口契约位于 [api/openapi.yaml](api/openapi.yaml)，一次性安装接口位于 [api/setup-openapi.yaml](api/setup-openapi.yaml)。安全与会话边界见 [架构说明](docs/architecture.md)，当前验证范围见 [验收记录](docs/verification.md)。

## 本地开发

需要 Go 1.25.14+、Node 22.18+、npm 和 Docker：

```bash
npm ci
npm run build
npm test
go test -race ./...
go vet ./...
./scripts/test-integration.sh
```

运行控制面预览：

```bash
./scripts/dev.sh
```

打开 `http://127.0.0.1:5188`，临时账号为 `preview@example.test`，密码为 `local-preview-password`。预览使用真实 API 和临时数据库，但不会模拟远程 Chrome。

构建可加载的 Chrome/Edge 扩展：

```bash
./scripts/package.sh
```

第三方组件与许可证来源见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
