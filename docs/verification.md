# 验收记录

日期：2026-09-14。状态：**首版源码与构建产物已交付；原生服务器运行验收尚未完成。**

## 已通过

| 检查 | 结果与边界 |
|---|---|
| Web / MV3 插件生产构建 | `npm run build` 成功，包含 OpenAPI 类型生成和 TypeScript 检查 |
| 插件实际加载 | 独立 Chromium 中加载 MV3 扩展，后台 worker、弹窗设置保存与打开目标标签均通过；测试目标由浏览器测试工具拦截，Chrome/Edge 工具栏右键流程仍可按安装说明人工验证 |
| 前端逻辑测试 | 网址 fragment 编码、非法地址拒绝；音频采样率转换、左右声道和队列上限 |
| Go 测试与静态检查 | `go test -race ./...`、`go vet ./...` 成功 |
| PostgreSQL 集成测试 | 真实 PostgreSQL 17；操作幂等、邀请复用拒绝、跨用户权限、CSRF、注销、旧 WebSocket 撤销、停止后旧控制权不能复活、休眠与缺失容器恢复均通过；Runner 使用测试替身 |
| Linux 文件边界测试 | 在 Linux 容器中验证文件下载、上传删除、符号链接与路径越界防护；测试不依赖浏览器镜像 |
| 跨平台编译 | Go 服务可编译为 Linux/amd64 |
| Compose / 网关 | Compose 配置校验、Caddy 配置校验通过 |
| 镜像构建 | API、Runner、Gateway 和 Linux/amd64 Browser 镜像均成功构建 |
| 依赖检查 | npm audit 为 0 项；以 Go 1.25.14 扫描，govulncheck 未发现可达漏洞。工具仍报告依赖模块中存在未被本项目调用的漏洞符号，不等于所有第三方代码都没有漏洞 |
| Web 实际操作 | 使用真实本地 API/数据库完成管理员登录、创建邀请、受邀账号激活；普通账号不显示管理入口 |
| 响应式页面 | 1440×1000 桌面、390×844 手机视口检查；手机页面 scrollWidth 等于 390，无横向溢出 |

本地环境为 macOS ARM64 + OrbStack。测试过程中修正了文件系统 symlink 处理、已完成操作幂等重试、控制权连接关闭、运行用户的 socket 目录权限，以及 KasmVNC 证书路径。

浏览器镜像当前构建包含 Google Chrome 153.0.8010.36、KasmVNC 1.5.0。Chrome 使用非默认的持久化目录，以满足调试端口对独立 user-data-dir 的要求。

## 运行环境阻塞

在 ARM 主机模拟运行 amd64 浏览器时，KasmVNC 已启动；Chrome 报告 namespace `clone: Invalid argument`，伴随 QEMU/ptrace 不支持错误，沙箱初始化失败。保留了非 root、no-new-privileges、seccomp 和 Chrome 沙箱要求，没有以关闭沙箱的方式绕过。

因此，**不能把此次运行标记为远程浏览器端到端通过**。运行日志保存在本地 `artifacts/container-smoke.log`。AppArmor 与宿主防火墙脚本没有在这台 macOS 主机上执行。

## 待在目标服务器执行

- `sudo ./scripts/smoke-browser.sh`：原生 x86_64 Chrome、VNC/CDP、网络阻断、正常停止与持久化挂载。
- `npm run acceptance`：配置三个测试账号和部署域名后，运行默认 60 分钟验收。当前未提供目标地址/账号，套件明确显示 **skipped**，不算通过。
- `./scripts/observe.sh 3600`：记录三用户场景 CPU、内存、网络与 OOM；确定 4 核/8 GB 是否达到容量目标。
- 人工验证中文组合输入、手机触摸与软键盘、网站文件选择器、浏览器 Cookie/标签页恢复，以及连续 30 分钟有声音视频播放。
- 验证真实 HTTPS、原生 AppArmor、IPv4/IPv6 网络阻断、DNS 重绑定/重定向访问私网，以及冷启动 ≤30 秒、重连 ≤5 秒的目标。
- 在备用服务器演练加密备份、恢复与镜像/Profile 配对回滚。
- 补充指定网站清单后，逐站验收登录、上传、下载和媒体能力。

这些指标是目标，不是已经测得的性能或兼容性结论。

## 产物

- `extension/dist/`：可加载的 Chrome/Edge MV3 扩展。
- `artifacts/cloud-browser-extension-0.1.0.zip`：插件安装包，解压后加载目录。
- `web/dist/`：Web 静态生产产物。
- `artifacts/screenshots/desktop.png`、`mobile.png`：本地控制面页面截图。
- `api/openapi.yaml`：HTTP 契约；`docs/deployment.md`：安装、备份、恢复与验收步骤。

## 原生服务器部署补充

已在原生 x86_64 Debian 13 服务器完成部署。单用户端到端测试、Chrome 实际音频信号、Cookie/localStorage/标签恢复及加密备份临时库恢复均已通过。之前关于“尚无原生运行验证”的描述属于本地开发阶段记录。3 用户长时间容量与视频、手机人工验收仍未完成。
