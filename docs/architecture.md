# 实现与信任边界

## 服务划分

`api` 负责账号、邀请、CSRF、数据库状态机和网关；`runner` 持有 Docker socket，只接受固定路径和 32 位十六进制用户 ID，执行参数均使用参数数组，没有 shell 拼接；`agent` 与 Chrome 同容器，使用非默认的 `~/.cloud-browser/chrome-profile` 目录，通过 loopback CDP 打开标签、通过 PulseAudio 采集声音。

生产 API 以 UID/GID 10001 运行。Runner socket 为 0660/root:10001；只有 API 挂载同一 socket volume。用户家目录以 1000:1000 保存，宿主父目录为管理员控制。

Browser Agent 拥有与用户浏览器相同的权限，不是用于隔离同一用户内部程序的安全沙箱；用户自己的浏览器扩展、网站沙箱漏洞或 Profile 文件改动可能破坏本人的数据。跨账号边界依赖容器、网桥、网关授权以及宿主机隔离。

## 身份与输入

- Argon2id 固定参数：64 MiB、2 次迭代、2 路并行；每个密码独立随机盐。
- 7 天会话 Cookie，生产为 Secure/HttpOnly/SameSite=Strict；数据库仅保存 token 摘要。
- 写操作校验完整 Origin；已登录写操作另需绑定登录会话的 CSRF token。
- 无公开注册；邀请明文只在创建时返回，数据库保存摘要。
- 扩展入口 `/#open=<encoded-url>` 在 React 初始化前读取和清除；登录前的待打开 URL 临时保存在该标签页 sessionStorage，提交后删除。
- `/browser/open` 只接受 HTTP/HTTPS，拒绝显式本地/私网 URL。DNS、重定向和站点发起的任意请求仍由宿主网络规则约束，不能用 URL 校验替代网络隔离。

## 异步操作与恢复

PostgreSQL 保存操作和会话。一个专用数据库连接持有调度器 advisory lock；失去锁连接时停止调度，避免另一个 API 实例接管后双重执行。首版仅支持一个 API 实例（连接撤销注册表位于进程内）。

`Idempotency-Key` 在用户范围内唯一，附带请求摘要。相同请求复用 operationId，不同请求返回 409。完成后 URL 正文清空；幂等记录保留 7 天。启动、停止通过确定的容器名实现重复调用安全。

Agent 在打开网址前写入 Profile 内的操作日志，再创建带 operation 标记的空标签，保存 targetId 后导航。无法确认操作时先检查 targetId 的当前 URL；不能确认则返回 UNKNOWN，不自动再次导航。该策略优先避免重复副作用；导航后又发生重定向或用户改变标签，可能需要人工判断。

正常停止发送 SIGTERM，Agent 调用 Browser.close，Docker 最多等待 30 秒；超时终止属于异常退出。磁盘数据可恢复，网页内存不可恢复。

## 控制权与连接

`POST /browser/takeover` 在锁定 session 行后更新 lease，并撤销进程内旧连接。lease 同时绑定用户、登录会话和随机 token。

- `GET /view/{lease}/vnc.html`：KasmVNC 页面。
- `GET /view/{lease}/websockify`：画面/输入 WebSocket，经 API 双向桥接。
- `GET /audio/{lease}`：PCM 音频 WebSocket。
- `GET /control/{lease}/status`：检查控制权。
- `POST /control/{lease}/input`：中文粘贴、剪贴板和白名单按键；需 CSRF。
- `GET /api/v1/events`：浏览器状态 SSE，不被计为观看连接。

HTTP lease 不能代替登录 Cookie。WebSocket 校验 Origin；控制权接管、注销和用户禁用会主动取消已有连接。每 2 秒再次验证数据库授权；画面连接另有 WebSocket ping，半开连接会被回收。只有有效画面连接刷新在线时间，音频、网页后台活动和仅打开首页不会阻止休眠。

KasmVNC 密码与 Agent token 只在内部服务之间使用。VNC iframe 保留原生工具栏，支持输入、触摸和剪贴板；外层工具提供便于手机操作的额外文本与按键入口。

## 文件与音频

文件列表仅展示 Uploads/Downloads 的普通文件，跳过临时下载和符号链接。文件 ID 是目录/文件名的 URL-safe 编码，不是权限凭据。下载始终按当前登录用户定位目录。实际打开通过持有的目录文件描述符与 openat(O_NOFOLLOW)，防止符号链接和路径逃逸。

上传以隐藏临时文件写入、fsync 后改名，失败清理；所有上传串行检查软限额，避免多个并发上传共同越过限额。浏览器本身写盘不受此软限额硬约束。

音频 48 kHz/s16le/双声道，每帧 20 ms。Agent 队列最多 5 帧，阻塞写入 250 ms 后断开，前端最多缓冲 250 ms，转换至实际 AudioContext 采样率。静音、退出、接管时销毁音频上下文与连接。前端有限重连 5 次，不无限积累音频。

## 已知限制

- 没有共享存储、跨节点切换、公开收费和 API 横向扩容。
- 没有严格的影音同步、DRM 保证、摄像头、麦克风或手机后台保活。
- 磁盘为软配额；数据库与 Profile 备份期间会暂时中断服务。
- 网站是否保留登录、允许数据中心 IP 或远程 Chrome，必须逐站验证。
