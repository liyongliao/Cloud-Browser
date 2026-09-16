# 部署与运维

## 主机要求

以 Ubuntu 24.04 或 Debian 13、x86_64、4 核、8 GB 内存、SSD 和 3 并发作为验证起点。管理程序会按需安装或复用 Docker Engine、Compose、AppArmor 和 iptables。Docker Desktop、rootless Docker、IPv6-only 出口和 nftables 原生 Docker 防火墙后端未纳入首版支持。

浏览器镜像不禁用 Chrome 沙箱、不请求特权模式。若宿主机禁止 user namespace，先修复主机策略，不能通过 `--no-sandbox` 或 privileged 绕过。

- API、数据库和网关位于控制网络。
- Runner 使用宿主网络以访问浏览器私有 IP，但仅监听受权限保护的 Unix socket。
- 每个浏览器只加入 `cloud-browser-egress`，不映射端口；ICC 关闭；出站私网、云元数据和本机地址由宿主机防火墙阻断。
- 浏览器容器内关闭 IPv6，宿主机额外拒绝浏览器桥接接口的 IPv6 流量。
- `.env`、Docker socket、宿主目录和 Runner 都属于管理员信任边界，不交给普通用户。

部署前确认 `172.30.0.0/24` 不与现有网络重叠。

## 首次安装

将域名 A 记录指向服务器，下载并校验发布文件：

```bash
curl -fLO https://github.com/liyongliao/Cloud-Browser/releases/latest/download/cloud-browser-linux-amd64
curl -fLO https://github.com/liyongliao/Cloud-Browser/releases/latest/download/cloud-browser-linux-amd64.sha256
sha256sum -c cloud-browser-linux-amd64.sha256
chmod +x cloud-browser-linux-amd64
sudo install -m 0755 cloud-browser-linux-amd64 /usr/local/bin/cloud-browser
sudo cloud-browser setup
```

向导只监听 `127.0.0.1:8090`，通过终端提示的 SSH 隧道访问。数据库必须选择内置、本机已有或远程之一。打开向导不会创建 PostgreSQL；仅在用户提交内置模式后创建项目专用服务。本机和远程模式会从应用容器网络验证连接与建表权限，不创建数据库服务或数据卷。

安装程序拉取摘要固定的预构建镜像，实际连接数据库后才创建表和管理员。管理员密码不会写入部署配置。安装完成后向导永久拒绝重复初始化并在 10 分钟后关闭。完整截图和数据库三种路径见 [首次安装教程](installation-guide.md)。

旧版 `/opt/cloud-browser` 源码部署会被管理程序识别并接入，不重建数据库、数据卷或管理员。

不要直接启用公开注册或将此单机版本当成恶意租户托管服务。

## 运行检查

```bash
sudo cloud-browser status
sudo cloud-browser doctor
sudo cloud-browser logs
sudo systemctl status cloud-browser-firewall
sudo iptables -S DOCKER-USER
sudo iptables -S CB-EGRESS
docker ps --filter label=cloud-browser.managed=true
docker stats --no-stream
```

管理页显示运行数、可用内存、磁盘空间。磁盘剩余不足 15% 时显示告警，并阻止新会话和上传。会话初始限制为内存 2 GiB、CPU 2 核、SHM 512 MiB、512 个进程；全机还必须保留至少 1.5 GiB 可用内存才能新启动。默认最多 3 个运行容器，`MAX_SESSIONS` 可降低上限。

新安装的运行文件位于 `/var/lib/cloud-browser`，Profile 位于 `/srv/cloud-browser/profiles`。降低会话限制不会主动停止现有会话。

KasmVNC 内部认证由网关注入；不要开启代理访问日志中的 Cookie、Authorization 或完整 `/view/<lease>` 路径。平台不记录完整网址；异步操作执行期间数据库短暂保存目标 URL，完成后清空正文，仅保留请求摘要用于幂等比较。浏览器历史仍在用户 Profile 内。

## 日常备份

安装 `age`，在可信设备上创建 age 密钥，将**公钥**提供给服务器，私钥离线保存。

```bash
sudo AGE_RECIPIENT='age1...' ./scripts/backup.sh
```

备份会临时停止 Web/API，正常关闭所有浏览器，然后导出 PostgreSQL 与完整 Profile，连同环境配置和浏览器镜像 ID 一起加密。最多保留最近 7 份，目录为 `/srv/cloud-browser/backups`。完成后仅恢复备份前正在运行的 Web/API；用户下次访问时重新启动浏览器。

将 `deploy/cloud-browser-backup.service` 与 `.timer` 安装到 systemd，编辑源码路径和公钥后，可启用每日 03:30 备份。第一次先手动运行，确认私钥可以解密。应另用现有传输工具把加密档案复制到独立设备；本项目不存储远端 SSH 私钥，也不会自动发送备份。

## 灾难恢复

1. 停止整个 Compose 栈；正常停止并移除带 `cloud-browser.managed=true` 标签的浏览器容器。确保没有进程写入 Profile。
2. 在权限为 0700 的临时目录中，用离线 age identity 解密备份并解包。**仅恢复自己产生且来源可信的归档**。
3. 先保留当前数据库与 Profile 的副本。将 `profiles.tar.gz` 恢复到 `/srv/cloud-browser`，保留 UID/GID（浏览器为 1000）。恢复环境配置，权限设为 0600。
4. 使用记录的浏览器镜像版本。先启动 PostgreSQL，执行：

```bash
docker compose -f compose.yaml -f compose.database.yaml up -d postgres
cat database.dump | docker compose -f compose.yaml -f compose.database.yaml exec -T postgres pg_restore -U cloudbrowser -d cloudbrowser --clean --if-exists
```

5. 重新执行主机准备脚本，启动 Runner/API/网关。调度器会校正数据库中的旧会话状态。逐用户确认标签页、登录、文件和接管行为。

恢复不是已自动验证的事务；在备用服务器演练成功前，不应删除原始备份。

## 升级与回滚

`v0.2.1` 管理程序暂不提供自动升级命令。升级前先停止新访问并完成数据库与 Profile 备份，阅读对应版本的发布说明，再使用发布文件中固定的镜像摘要更新。不要把 `latest` 作为可回滚版本记录。

回滚 Chrome 必须同时恢复该镜像升级前的 Profile；直接把新版本 Profile 挂给旧 Chrome 可能失败。不要同时用两个容器挂载同一用户目录。自动升级和自动回滚会在后续版本单独设计。

## 目标服务器验收

先执行原生容器检查（要求已配置 AppArmor 与防火墙）：

```bash
sudo ./scripts/smoke-browser.sh
```

该检查验证 Chrome/CDP 和 KasmVNC 启动、内部鉴权、基本网址操作、私网阻断和持久化挂载，不等于已经验证网站 Cookie 或影音体验。

再创建 3 个专用受邀测试账号。将账号写入本地 `acceptance-users.json`，格式为含 3 项的 `[{"email":"...","password":"..."}]`，设为 0600；该文件已被 Git 忽略。不要使用正在工作的真实用户账号，测试结束会停止这三个浏览器。

```bash
npx playwright install chromium
ACCEPTANCE_ORIGIN=https://browser.example.com \
ACCEPTANCE_USERS_FILE=./acceptance-users.json \
ACCEPTANCE_SECONDS=3600 \
ACCEPTANCE_URLS=https://example.com \
npm run acceptance
```

测试会通过真实 Web 登录并启动 3 个浏览器，检查画面/音频 WebSocket 收到数据、文件 API 往返、控制权接管与幂等操作，持续默认 60 分钟。音频收到数据也可能是静音，仍需人工验证实际声音、中文输入、视频持续播放和影音延迟。文件 API 往返不代替网站文件选择器的人工操作验证。

同时在服务器执行 `./scripts/observe.sh 3600`，记录 Docker CPU、内存、网络和 OOM 状态到 `artifacts/`。测试报告位于 `artifacts/acceptance-report`。没有配置部署地址和账号时套件明确跳过，不会伪装通过。

## 已有 Nginx 的服务器

保留已有 HTTPS 入口，在 `.env` 额外设置：

```dotenv
GATEWAY_SITE=:80
GATEWAY_HTTP_BIND=127.0.0.1:18088
GATEWAY_HTTPS_BIND=127.0.0.1:18443
```

Nginx 的独立域名虚拟主机反代至 `http://127.0.0.1:18088`，保留 Host，启用 WebSocket Upgrade、关闭代理缓冲，设置 `client_max_body_size 201m` 与长连接超时。`DOMAIN` 仍为外部 HTTPS 域名。两个网关端口均仅绑定 loopback。

若云服务器公网 IP 通过供应商 NAT 映射，不出现在本机网卡，将这些地址逐行写入 `/etc/cloud-browser/public-ipv4`，再运行 `scripts/prepare-host.sh`，以阻止浏览器绕道公网访问宿主服务。

小规格主机单用户验收可设置 `ACCEPTANCE_CONCURRENCY=1`，账号文件对应 1 项；该结果不代表通过 3 用户容量测试。默认仍为 3 用户。

Chrome 沙箱会在已隔离的用户命名空间内调用 `chroot`。定制 seccomp 允许此调用，内核仍要求调用者具备对应命名空间权限；容器保持 `cap_drop=ALL` 和 `no-new-privileges`。
