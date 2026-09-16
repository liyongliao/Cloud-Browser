# 首次安装教程

Cloud Browser 通过一个 Linux 可执行文件完成安装和日常管理。服务器无需下载源码、安装编译器或手工编写 `.env`。程序内部使用 Docker 运行正式服务，但会自动检查和管理所需组件。

## 1. 准备服务器和域名

首版支持 Ubuntu 24.04、Debian 13 和 x86_64，推荐至少 2 核、4 GB 内存、25 GB 可用 SSD。4 GB 内存请选择 1 个并发会话；3 个会话建议 4 核、8 GB 内存。

将域名 A 记录指向服务器。自动 HTTPS 需要允许 TCP 80、443；安装向导只监听服务器的 `127.0.0.1:8090`，不需要开放公网端口。

## 2. 下载单文件程序

```bash
curl -fLO https://github.com/liyongliao/Cloud-Browser/releases/latest/download/cloud-browser-linux-amd64
curl -fLO https://github.com/liyongliao/Cloud-Browser/releases/latest/download/cloud-browser-linux-amd64.sha256
sha256sum -c cloud-browser-linux-amd64.sha256
chmod +x cloud-browser-linux-amd64
sudo install -m 0755 cloud-browser-linux-amd64 /usr/local/bin/cloud-browser
sudo cloud-browser setup
```

程序会在后台启动安装向导并输出两行命令。先在自己的电脑建立 SSH 转发：

```bash
ssh -L 8090:127.0.0.1:8090 root@服务器地址
```

然后打开终端输出的 `http://127.0.0.1:8090/#token=...`。令牌只进入浏览器当前标签页，页面读取后立即从地址栏清除。SSH 断开或关闭网页不会中止已经提交的服务器安装；重新运行 `sudo cloud-browser setup` 可以取得当前向导链接。

此时还没有安装、拉取或启动 PostgreSQL。打开页面、查看状态和填写表单都不会创建数据库。

## 3. 选择数据库来源

数据库是第一步必选项：

![数据库来源选择](images/setup-database.png)

### 内置 PostgreSQL

适合没有现成数据库的个人服务器。无需填写数据库名、用户或密码。只有点击最后的“开始安装”后，程序才会：

1. 生成并保存项目专用数据库凭据；
2. 下载固定版本的 PostgreSQL 镜像；
3. 创建 `cloud-browser-postgres-1` 和 `cloud-browser-database`；
4. 从应用容器网络验证连接后初始化表。

安装失败后重新提交会复用同一个容器、数据卷和凭据，不会创建第二套 PostgreSQL。

### 本机已有 PostgreSQL

这里的“本机”是运行 Cloud Browser 的 Linux 服务器。填写端口、专用数据库名、账号和密码后，点击“测试连接与权限”。程序通过 Docker 宿主网关从应用容器网络测试，不会安装 PostgreSQL、拉取 PostgreSQL 镜像或创建数据卷。

PostgreSQL 如果只监听 `127.0.0.1`，Docker 容器无法访问。请让它监听服务器的 Docker 网桥可达地址，并在 `pg_hba.conf` 中只允许相应 Docker 网段和专用账号。安装程序会提示这个问题，但不会修改已有 PostgreSQL 配置。

### 远程 PostgreSQL

填写主机、端口、数据库名、账号、密码和 TLS 模式。推荐 `require`；需要严格验证证书主机名时使用 `verify-full`。远程数据库须提前创建空的专用数据库，账号需要连接及建表权限。

“测试连接与权限”会在事务中临时建表并回滚，不保留测试表。密码错误、TLS 错误或权限不足时会留在当前步骤，绝不自动改用内置数据库。

## 4. 完成网页向导

第二步设置访问域名。空白服务器选择“自动 HTTPS”；已有 Nginx、Traefik 等网关时选择“已有反向代理”，正式服务只监听 `127.0.0.1:18088`。

第三步创建管理员，密码至少 12 个字符。密码通过 Argon2id 保存摘要，不写入部署配置或日志。

第四步选择 1–3 个并发会话并确认摘要：

![安装配置确认](images/setup-summary.png)

提交后，程序才检查或安装 Docker、准备隔离规则、按数据库选择下载所需镜像、初始化管理员并启动服务。正式发布版使用固定镜像摘要，不在服务器编译代码或浏览器镜像。

![安装完成](images/setup-complete.png)

成功后一次性安装接口失效，并在 10 分钟后关闭。失败时重新打开向导即可查看阶段和修改配置。若本次安装已经创建了内置 PostgreSQL，重试会锁定为内置来源，避免遗留另一套数据库。

## 5. 已有反向代理

选择“已有反向代理”后，将外部 HTTPS 请求转发到 `http://127.0.0.1:18088`。代理必须保留 Host 和 WebSocket Upgrade、关闭响应缓冲、允许 201 MB 请求体，并设置长连接超时。仓库中的 [Nginx 示例](../deploy/nginx.conf.example)可直接参考。

## 6. 安装后管理

```bash
sudo cloud-browser status   # 部署目录、数据库来源、访问地址和容器状态
sudo cloud-browser start    # 启动应用；本机/远程数据库不受影响
sudo cloud-browser stop     # 先正常停止浏览器，再停止本项目服务
sudo cloud-browser logs     # 查看持续日志
sudo cloud-browser doctor   # 检查系统、Docker、Compose 和部署识别
sudo cloud-browser version
```

`stop` 不删除数据库卷和 Profile。内置 PostgreSQL 跟随应用启停；本机或远程 PostgreSQL 永远不由该命令管理。

旧版源码部署位于 `/opt/cloud-browser` 时，首次运行新程序会识别并记录现有目录，然后显示原服务状态。它不会重新创建管理员、数据库、数据卷或升级镜像。

## 常见问题

“数据库连接失败”：先区分本机与远程。本机实例检查 `listen_addresses`、`pg_hba.conf` 和 Docker 网段；远程实例检查服务器出口 IP、云防火墙、账号权限和 TLS 配置。

“域名无法申请证书”：确认 DNS 已指向服务器公网 IP，80/443 未被其他服务占用，云防火墙允许访问。已有其他网关时改选反向代理模式。

“安装页面关闭了”：运行 `sudo cloud-browser setup` 重新显示链接。安装在后台运行，日志位于 `/var/lib/cloud-browser/setup.log`。

“外部数据库如何备份”：使用数据库服务商快照或独立 `pg_dump`。Cloud Browser 不停止或备份本机已有及远程数据库；Profile 仍应在浏览器停止后备份。

“服务器只有 4 GB 内存”：选择 1 个会话。如果同机还有其他服务，应增加内存或迁移服务。
