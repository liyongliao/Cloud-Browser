import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Cloud,
  ArrowUpRight,
  ArrowRight,
  Globe,
  Link as LinkIcon,
  Monitor,
  Smartphone,
  ShieldCheck,
  LogOut,
  Play,
  Square,
  Volume2,
  VolumeX,
  Files,
  Upload,
  Download,
  Trash2,
  Maximize,
  Keyboard,
  Clipboard,
  Settings,
  X,
  LoaderCircle,
  Check,
  RefreshCw,
  Plus,
  Users,
  KeyRound,
} from "lucide-react";
import {
  APIError,
  request,
  operate,
  setCSRF,
  type User,
  type Browser,
  type Lease,
  type FileEntry,
} from "./api";
import { startAudio } from "./audio";
import "./style.css";
const hash = new URLSearchParams(location.hash.slice(1));
const incoming = hash.get("open");
const invitation = hash.get("invite");
if (location.hash)
  history.replaceState(null, "", location.pathname + location.search);
if (incoming) sessionStorage.setItem("cb_pending_url", incoming);
const labels: Record<string, string> = {
  STOPPED: "已休眠",
  STARTING: "正在启动",
  RUNNING: "运行中",
  STOPPING: "正在保存",
  FAILED: "需要重启",
};
function App() {
  const [user, setUser] = useState<User | null>(null),
    [loaded, setLoaded] = useState(false),
    [browser, setBrowser] = useState<Browser | null>(null),
    [lease, setLease] = useState<Lease | null>(null),
    [busy, setBusy] = useState(""),
    [error, setError] = useState(""),
    [page, setPage] = useState("browser"),
    [url, setURL] = useState(""),
    [panel, setPanel] = useState(""),
    [fileData, setFileData] = useState<{
      files: FileEntry[];
      usedBytes: number;
      softLimitBytes: number;
    } | null>(null),
    [audio, setAudio] = useState(false),
    [audioState, setAudioState] = useState(""),
    [text, setText] = useState("");
  const soundStop = useRef<(() => void) | null>(null),
    viewer = useRef<HTMLDivElement>(null),
    pendingConsumed = useRef(false);
  async function run(task: () => Promise<void>, message = "正在处理…") {
    if (busy) return;
    setBusy(message);
    setError("");
    try {
      await task();
    } catch (e) {
      setError(e instanceof Error ? e.message : "操作失败");
      if (e instanceof APIError && e.status === 401) {
        setUser(null);
        disconnect();
      }
    } finally {
      setBusy("");
    }
  }
  async function refresh() {
    setBrowser(await request<Browser>("/api/v1/browser"));
  }
  async function loadFiles() {
    try {
      setFileData(await request("/api/v1/files"));
    } catch (e) {
      if (!(e instanceof APIError && e.code === "PROFILE_NOT_INITIALIZED"))
        throw e;
    }
  }
  async function identify() {
    const me = await request<User>("/api/v1/me");
    setCSRF(me.csrf);
    setUser(me);
  }
  useEffect(() => {
    identify()
      .catch(() => {})
      .finally(() => setLoaded(true));
  }, []);
  useEffect(() => {
    if (!user) return;
    void refresh().catch((e) => setError(e.message));
    void loadFiles().catch(() => {});
    const events = new EventSource("/api/v1/events");
    events.onmessage = (e) => {
      const next = JSON.parse(e.data) as { state: Browser["state"] };
      setBrowser((old) => (old ? { ...old, state: next.state } : old));
      if (next.state !== "RUNNING") disconnect();
    };
    events.onerror = () => {
      void refresh().catch(() => {});
    };
    if (!pendingConsumed.current) {
      pendingConsumed.current = true;
      const pending = sessionStorage.getItem("cb_pending_url");
      if (pending) {
        sessionStorage.removeItem("cb_pending_url");
        setURL(pending);
        void run(() => connect(pending), "正在云端打开链接…");
      }
    }
    return () => events.close();
  }, [user?.id]);
  useEffect(() => {
    if (!lease) return;
    let live = true;
    const timer = setInterval(() => {
      void request(`/control/${lease.lease}/status`).catch((e) => {
        if (
          live &&
          e instanceof APIError &&
          (e.status === 409 || e.status === 401)
        ) {
          disconnect();
          setError("连接已结束或已在另一台设备接管。");
        }
      });
    }, 5000);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [lease?.lease]);
  useEffect(() => () => soundStop.current?.(), []);
  function disconnect() {
    soundStop.current?.();
    soundStop.current = null;
    setAudio(false);
    setLease(null);
  }
  async function connect(target?: string) {
    const state = await request<Browser>("/api/v1/browser");
    if (state.state === "FAILED") await operate("stop");
    if (target) {
      let parsed: URL;
      try {
        parsed = new URL(target.includes("://") ? target : "https://" + target);
      } catch {
        throw new Error("请输入有效网址。");
      }
      if (!["http:", "https:"].includes(parsed.protocol))
        throw new Error("仅支持 HTTP/HTTPS 网址。");
      await operate("open", parsed.href);
    } else await operate("start");
    const next = await request<Lease>("/api/v1/browser/takeover", "POST", {});
    disconnect();
    setLease(next);
    await refresh();
    void loadFiles();
  }
  async function input(body: object) {
    if (!lease) return;
    try {
      return await request<{ text?: string }>(
        `/control/${lease.lease}/input`,
        "POST",
        body,
      );
    } catch (e) {
      if (e instanceof APIError && e.code === "CONTROL_REVOKED") disconnect();
      throw e;
    }
  }
  if (!loaded)
    return (
      <div className="loading">
        <Cloud />
        <p>正在连接你的浏览器…</p>
      </div>
    );
  if (!user)
    return <Login invitation={invitation} onLogin={identify} error={error} />;
  return (
    <div className="app">
      <aside className="sidebar">
        <a className="brand" href="/">
          <span className="brand-mark">
            <Cloud size={24} />
          </span>
          <span>
            Cloud Browser<small>让浏览，自由发生</small>
          </span>
        </a>
        <div className="nav-caption">工作空间</div>
        <nav>
          <button
            className={page === "browser" ? "nav active" : "nav"}
            onClick={() => setPage("browser")}
          >
            <Globe size={19} />
            我的浏览器
            <span className="nav-dot" />
          </button>
          <button
            className={page === "files" ? "nav active" : "nav"}
            onClick={() => {
              setPage("files");
              void run(loadFiles);
            }}
          >
            <Files size={19} />
            我的文件
          </button>
          <button
            className={page === "account" ? "nav active" : "nav"}
            onClick={() => setPage("account")}
          >
            <KeyRound size={19} />
            账号设置
          </button>
          {user.admin && (
            <button
              className={page === "admin" ? "nav active" : "nav"}
              onClick={() => setPage("admin")}
            >
              <Settings size={19} />
              管理空间
            </button>
          )}
        </nav>
        <div className="sidebar-note">
          <span className="note-icon">
            <Smartphone size={22} />
            <ArrowUpRight size={16} />
          </span>
          <strong>下一站，接着浏览。</strong>
          <p>换一台设备，熟悉的标签页和收藏夹仍在这里。</p>
          <span>电脑 · 平板 · 手机</span>
        </div>
        <div className="account">
          <span className="avatar">{user.email[0].toUpperCase()}</span>
          <div>
            <strong>{user.email.split("@")[0]}</strong>
            <small>{user.admin ? "空间管理员" : "个人空间"}</small>
          </div>
          <button
            className="icon"
            title="退出登录"
            onClick={() =>
              void run(async () => {
                await request("/api/v1/auth/logout", "POST", {});
                disconnect();
                setUser(null);
                pendingConsumed.current = false;
              })
            }
          >
            <LogOut size={17} />
          </button>
        </div>
      </aside>
      <main>
        <header className="topbar">
          <div>
            <span>个人空间</span>
            <span className="slash">/</span>
            <strong>
              {page === "admin"
                ? "管理空间"
                : page === "files"
                  ? "我的文件"
                  : page === "account"
                    ? "账号设置"
                    : "我的浏览器"}
            </strong>
          </div>
          <div className="topbar-right">
            <span className="private-label">
              <ShieldCheck size={15} />
              独立浏览空间
            </span>
            <button
              className="icon mobile-logout"
              aria-label="退出登录"
              onClick={() =>
                void run(async () => {
                  await request("/api/v1/auth/logout", "POST", {});
                  disconnect();
                  setUser(null);
                  pendingConsumed.current = false;
                })
              }
            >
              <LogOut size={15} />
            </button>
          </div>
        </header>
        <div className="content">
          {error && (
            <div className="alert" role="alert">
              {error}
              <button
                className="icon"
                onClick={() => setError("")}
                aria-label="关闭提示"
              >
                <X size={16} />
              </button>
            </div>
          )}
          {busy && (
            <div className="progress" role="status">
              <LoaderCircle className="spin" size={16} />
              {busy}
            </div>
          )}
          {page === "browser" && (
            <>
              <div className="heading">
                <div className="eyebrow">YOUR BROWSER, EVERYWHERE</div>
                <h1>
                  你的浏览器，随处继续<span>。</span>
                </h1>
                <p>标签页、收藏夹和浏览数据，都保留在你的云端空间。</p>
              </div>
              <form
                className="url-input"
                onSubmit={(e) => {
                  e.preventDefault();
                  void run(() => connect(url), "正在打开链接…");
                }}
              >
                <LinkIcon size={20} />
                <input
                  aria-label="在云端打开网址"
                  placeholder="输入网址，在云端打开"
                  value={url}
                  onChange={(e) => setURL(e.target.value)}
                  required
                />
                <button disabled={!!busy} type="submit">
                  打开
                  <ArrowUpRight size={18} />
                </button>
              </form>
              {!lease ? (
                <>
                  <section className="browser-card">
                    <div className="card-top">
                      <span
                        className={
                          "status " +
                          (browser?.state === "RUNNING" ? "live" : "")
                        }
                      >
                        <i />
                        {labels[browser?.state ?? "STOPPED"]}
                      </span>
                      <span className="card-id">PERSONAL BROWSER / 01</span>
                    </div>
                    <div className="card-body">
                      <div>
                        <span className="browser-symbol">
                          <Globe size={30} />
                        </span>
                        <h2>我的云端浏览器</h2>
                        <p>
                          {browser?.state === "RUNNING"
                            ? "浏览器已经就绪，接管后即可继续操作。"
                            : "熟悉的浏览环境，等你回来。"}
                        </p>
                        <button
                          className="primary"
                          disabled={!!busy}
                          onClick={() =>
                            void run(() => connect(), "正在准备你的浏览器…")
                          }
                        >
                          {browser?.state === "RUNNING"
                            ? "接管并继续浏览"
                            : "启动并继续浏览"}
                          <ArrowRight size={18} />
                        </button>
                      </div>
                      <BrowserIllustration />
                    </div>
                    <div className="card-footer">
                      <span>
                        <ShieldCheck size={16} />
                        个人数据持续保留
                      </span>
                      <span>
                        <Monitor size={16} />
                        真实 Chrome 浏览器
                      </span>
                    </div>
                  </section>
                  <div className="section-label">
                    <h2>为下一次浏览准备就绪</h2>
                    <span>你的空间，随身携带</span>
                  </div>
                  <div className="feature-grid">
                    <Feature
                      icon={<Monitor />}
                      title="从任意设备出发"
                      text="网页登录即可访问；使用扩展，一键将当前网页送到云端。"
                    />
                    <Feature
                      icon={<Files />}
                      title="文件，来去自如"
                      text="上传到云端，再从浏览器选择；下载的文件也可保存到本地。"
                    />
                    <Feature
                      icon={<ShieldCheck />}
                      title="安心保存，轻松继续"
                      text="断连 10 分钟后停止运行并保留数据。重新访问时恢复浏览器。"
                    />
                  </div>
                  <div className="footnote">
                    <span className="dot" />
                    每个账号拥有独立浏览器<span>·</span>
                    休眠后网页任务与未完成下载将停止
                  </div>
                </>
              ) : (
                <section className="viewer" ref={viewer}>
                  <div className="viewer-tools">
                    <span className="status live">
                      <i />
                      已连接
                    </span>
                    <div className="tool-actions">
                      <button
                        onClick={() =>
                          void run(async () => {
                            if (audio) {
                              soundStop.current?.();
                              soundStop.current = null;
                              setAudio(false);
                            } else {
                              soundStop.current = await startAudio(
                                lease.audioUrl,
                                setAudioState,
                              );
                              setAudio(true);
                            }
                          })
                        }
                        title={audioState}
                      >
                        {audio ? <Volume2 size={17} /> : <VolumeX size={17} />}
                        声音
                      </button>
                      <button
                        onClick={() =>
                          setPanel(panel === "input" ? "" : "input")
                        }
                      >
                        <Keyboard size={17} />
                        输入
                      </button>
                      <button
                        onClick={() => {
                          setPanel(panel === "files" ? "" : "files");
                          void run(loadFiles);
                        }}
                      >
                        <Files size={17} />
                        文件
                      </button>
                      <button
                        onClick={() =>
                          void viewer.current
                            ?.requestFullscreen()
                            .catch(() =>
                              setError("当前设备不支持全屏，请旋转屏幕浏览。"),
                            )
                        }
                        title="全屏"
                      >
                        <Maximize size={17} />
                      </button>
                      <button
                        onClick={() =>
                          void run(async () => {
                            disconnect();
                            await operate("stop");
                            await refresh();
                          }, "正在保存并停止浏览器…")
                        }
                      >
                        <Square size={15} />
                        停止
                      </button>
                    </div>
                  </div>
                  <iframe
                    title="远程 Chrome 浏览器"
                    src={lease.viewerUrl}
                    allow="clipboard-read; clipboard-write; fullscreen"
                    referrerPolicy="no-referrer"
                  />
                  <div className="viewer-hint">
                    触摸、缩放与剪贴板选项可在画面侧边菜单调整。切换设备后，点击“接管并继续浏览”。
                    <button onClick={disconnect}>断开连接</button>
                  </div>
                  {panel === "input" && (
                    <div className="input-panel">
                      <textarea
                        aria-label="发送到远程浏览器的文本"
                        placeholder="在这里输入中文，然后粘贴到云端当前输入框"
                        value={text}
                        onChange={(e) => setText(e.target.value)}
                        maxLength={12000}
                      />
                      <button
                        onClick={() =>
                          void run(async () => {
                            await input({ text, action: "paste" });
                          })
                        }
                      >
                        <Clipboard size={16} />
                        粘贴到云端
                      </button>
                      <button
                        onClick={() =>
                          void run(async () => {
                            const result = await input({
                              action: "clipboard-read",
                            });
                            setText(result?.text ?? "");
                          })
                        }
                      >
                        读取云端剪贴板
                      </button>
                      <div className="keys">
                        {[
                          "Return",
                          "Escape",
                          "Tab",
                          "BackSpace",
                          "ctrl+l",
                          "ctrl+t",
                          "ctrl+w",
                          "alt+Left",
                          "F5",
                        ].map((key) => (
                          <button
                            key={key}
                            onClick={() =>
                              void run(async () => {
                                await input({ key });
                              })
                            }
                          >
                            {key}
                          </button>
                        ))}
                      </div>
                    </div>
                  )}
                  {panel === "files" && (
                    <FilePanel data={fileData} run={run} reload={loadFiles} />
                  )}
                </section>
              )}
            </>
          )}
          {page === "files" && (
            <>
              <div className="heading">
                <div className="eyebrow">YOUR FILES</div>
                <h1>云端与本地，自由传递。</h1>
                <p>上传到 Uploads，网站下载保存在 Downloads。</p>
              </div>
              <FilePanel data={fileData} run={run} reload={loadFiles} />
            </>
          )}
          {page === "account" && <AccountSettings user={user} />}
          {page === "admin" && user.admin && <Admin run={run} busy={!!busy} />}
        </div>
      </main>
    </div>
  );
}
function AccountSettings({ user }: { user: User }) {
  const [currentPassword, setCurrentPassword] = useState(""),
    [newPassword, setNewPassword] = useState(""),
    [confirmation, setConfirmation] = useState(""),
    [busy, setBusy] = useState(false),
    [message, setMessage] = useState(""),
    [success, setSuccess] = useState(false);
  return (
    <>
      <div className="heading">
        <div className="eyebrow">ACCOUNT SECURITY</div>
        <h1>管理账号与登录安全。</h1>
        <p>修改密码后，其他设备上的登录会立即失效。</p>
      </div>
      <section className="account-settings">
        <div className="account-identity">
          <span className="avatar">{user.email[0].toUpperCase()}</span>
          <div>
            <small>当前账号</small>
            <strong>{user.email}</strong>
            <span>{user.admin ? "空间管理员" : "个人用户"}</span>
          </div>
        </div>
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            setMessage("");
            setSuccess(false);
            if (newPassword !== confirmation) {
              setMessage("两次输入的新密码不一致。");
              return;
            }
            setBusy(true);
            try {
              await request("/api/v1/auth/password", "POST", {
                currentPassword,
                newPassword,
              });
              setCurrentPassword("");
              setNewPassword("");
              setConfirmation("");
              setSuccess(true);
              setMessage("密码已更新，其他设备需要重新登录。");
            } catch (error) {
              setMessage(
                error instanceof Error ? error.message : "密码修改失败。",
              );
            } finally {
              setBusy(false);
            }
          }}
        >
          <h2>修改密码</h2>
          <label>
            当前密码
            <input
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
              required
            />
          </label>
          <label>
            新密码
            <input
              type="password"
              autoComplete="new-password"
              minLength={12}
              maxLength={256}
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
              required
            />
            <small>至少 12 个字符，建议使用密码管理器生成。</small>
          </label>
          <label>
            再次输入新密码
            <input
              type="password"
              autoComplete="new-password"
              minLength={12}
              maxLength={256}
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              required
            />
          </label>
          {message && (
            <div
              className={success ? "notice success" : "alert"}
              role={success ? "status" : "alert"}
            >
              {message}
            </div>
          )}
          <button className="primary" disabled={busy}>
            {busy ? (
              <LoaderCircle className="spin" size={17} />
            ) : (
              <KeyRound size={17} />
            )}
            保存新密码
          </button>
        </form>
      </section>
    </>
  );
}
function Login({
  invitation,
  onLogin,
  error,
}: {
  invitation: string | null;
  onLogin: () => Promise<void>;
  error: string;
}) {
  const [email, setEmail] = useState(""),
    [password, setPassword] = useState(""),
    [busy, setBusy] = useState(false),
    [message, setMessage] = useState(error);
  return (
    <div className="login">
      <div className="login-story">
        <span className="brand">
          <Cloud /> Cloud Browser
        </span>
        <h1>
          熟悉的浏览器，
          <br />
          不止一台设备。
        </h1>
        <p>
          把标签页、收藏夹和日常浏览留在云端。
          <br />
          下一次，从任何地方继续。
        </p>
        <BrowserIllustration />
        <span className="login-foot">YOUR BROWSER, EVERYWHERE.</span>
      </div>
      <form
        className="login-form"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setMessage("");
          try {
            await request(
              "/api/v1/auth/" + (invitation ? "activate" : "login"),
              "POST",
              { email, password, ...(invitation ? { token: invitation } : {}) },
            );
            await onLogin();
          } catch (e) {
            setMessage(e instanceof Error ? e.message : "登录失败");
          } finally {
            setBusy(false);
          }
        }}
      >
        <span className="browser-symbol">
          <Cloud />
        </span>
        <h2>{invitation ? "开启你的云端空间" : "欢迎回来"}</h2>
        <p>
          {invitation
            ? "设置账号与密码，接受邀请。"
            : "登录你的账号，继续上次的浏览。"}
        </p>
        <label>
          邮箱
          <input
            type="email"
            autoComplete="username"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
        <label>
          密码
          <input
            type="password"
            autoComplete={invitation ? "new-password" : "current-password"}
            minLength={invitation ? 12 : undefined}
            maxLength={256}
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {message && (
          <div className="alert" role="alert">
            {message}
          </div>
        )}
        <button className="primary" disabled={busy}>
          {busy ? (
            <LoaderCircle className="spin" size={18} />
          ) : (
            <ArrowRight size={18} />
          )}{" "}
          {invitation ? "接受邀请" : "登录并继续"}
        </button>
        <small>仅限受邀用户 · 浏览数据保存在你的专属空间</small>
      </form>
    </div>
  );
}
function BrowserIllustration() {
  return (
    <div className="illustration" aria-hidden="true">
      <div className="orbit orbit-one" />
      <div className="orbit orbit-two" />
      <div className="mini-window">
        <div className="mini-tabs">
          <i />
          <i />
          <i />
          <div />
          <Plus size={11} />
        </div>
        <div className="mini-address">
          <ShieldCheck size={10} />
          <span>你的个人浏览空间</span>
        </div>
        <div className="mini-body">
          <span>
            <Cloud size={34} />
          </span>
          <div className="mini-search" />
          <div className="mini-shortcuts">
            <i />
            <i />
            <i />
            <i />
          </div>
        </div>
      </div>
      <span className="saved-chip">
        <Check size={12} />
        随时继续
      </span>
    </div>
  );
}
function Feature({
  icon,
  title,
  text,
}: {
  icon: React.ReactNode;
  title: string;
  text: string;
}) {
  return (
    <article className="feature">
      <span>{icon}</span>
      <h3>{title}</h3>
      <p>{text}</p>
    </article>
  );
}
type Runner = (task: () => Promise<void>, message?: string) => Promise<void>;
function bytes(n: number) {
  return n >= 1 << 30
    ? (n / (1 << 30)).toFixed(1) + " GB"
    : n >= 1 << 20
      ? (n / (1 << 20)).toFixed(1) + " MB"
      : Math.ceil(n / 1024) + " KB";
}
function FilePanel({
  data,
  run,
  reload,
}: {
  data: {
    files: FileEntry[];
    usedBytes: number;
    softLimitBytes: number;
  } | null;
  run: Runner;
  reload: () => Promise<void>;
}) {
  const ref = useRef<HTMLInputElement>(null);
  return (
    <section className="file-panel">
      <div className="file-header">
        <div>
          <h2>我的文件</h2>
          <p>
            {data
              ? `${bytes(data.usedBytes)} / ${bytes(data.softLimitBytes)} · 单次上传最多 200 MB`
              : "首次启动浏览器后即可上传文件"}
          </p>
        </div>
        <div className="tool-actions">
          <button onClick={() => void run(reload)} title="刷新文件">
            <RefreshCw size={17} />
          </button>
          <button
            className="primary"
            disabled={!data}
            onClick={() => ref.current?.click()}
          >
            <Upload size={16} />
            上传文件
          </button>
        </div>
        <input
          ref={ref}
          type="file"
          hidden
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file)
              void run(async () => {
                if (file.size > 200 * 1024 * 1024)
                  throw new Error("文件不能超过 200 MB");
                const form = new FormData();
                form.append("file", file);
                await request("/api/v1/files", "POST", form);
                await reload();
              }, "正在上传文件…");
            e.target.value = "";
          }}
        />
      </div>
      {data?.files.length ? (
        <div className="file-list">
          {data.files.map((f) => (
            <div className="file-row" key={f.id}>
              <Files size={20} />
              <div>
                <strong>{f.name}</strong>
                <small>
                  {f.directory} · {bytes(f.size)}
                </small>
              </div>
              <a
                className="icon"
                href={"/api/v1/files/" + f.id + "/download"}
                title={"下载 " + f.name}
              >
                <Download size={18} />
              </a>
              <button
                className="icon"
                title={"删除 " + f.name}
                onClick={() => {
                  if (window.confirm(`删除 ${f.name}？此操作无法撤销。`))
                    void run(async () => {
                      await request("/api/v1/files/" + f.id, "DELETE");
                      await reload();
                    });
                }}
              >
                <Trash2 size={17} />
              </button>
            </div>
          ))}
        </div>
      ) : (
        <div className="empty">
          <Files size={34} />
          <h3>这里还没有文件</h3>
          <p>上传一个文件，或在云端浏览器中下载。</p>
        </div>
      )}
    </section>
  );
}
function Admin({ run, busy }: { run: Runner; busy: boolean }) {
  const [users, setUsers] = useState<
      Array<User & { disabled: boolean; state: string }>
    >([]),
    [invite, setInvite] = useState(""),
    [resources, setResources] = useState<{
      running: number;
      maxSessions: number;
      diskFreeFraction: number;
      memoryAvailableBytes: number;
      diskLow: boolean;
    } | null>(null);
  async function refresh() {
    setUsers(await request("/api/v1/admin/users"));
    setResources(await request("/api/v1/admin/resources"));
  }
  useEffect(() => {
    void run(refresh);
  }, []);
  return (
    <>
      <div className="heading">
        <div className="eyebrow">SPACE ADMINISTRATION</div>
        <h1>管理你的共享空间。</h1>
        <p>邀请可信用户，并留意服务器的运行容量。</p>
      </div>
      <div className="admin-summary">
        <Users />
        <strong>
          {resources
            ? `${resources.running} / ${resources.maxSessions} 个浏览器运行中`
            : "正在读取容量"}
        </strong>
        <span>
          {resources
            ? `磁盘可用 ${Math.round(resources.diskFreeFraction * 100)}% · 可用内存 ${bytes(resources.memoryAvailableBytes)}`
            : ""}
        </span>
        <button onClick={() => void run(refresh)}>
          <RefreshCw size={16} />
          刷新
        </button>
      </div>
      {resources?.diskLow && (
        <div className="alert">磁盘剩余不足 15%，新会话和上传已暂停。</div>
      )}
      <button
        className="primary"
        disabled={busy}
        onClick={() =>
          void run(async () => {
            const result = await request<{ url: string }>(
              "/api/v1/admin/invites",
              "POST",
              {},
            );
            setInvite(result.url);
          })
        }
      >
        <Plus size={17} />
        创建邀请
      </button>
      {invite && (
        <div className="invite-box">
          <label>
            48 小时内有效，仅能使用一次
            <input readOnly value={invite} onFocus={(e) => e.target.select()} />
          </label>
          <button onClick={() => void navigator.clipboard.writeText(invite)}>
            复制链接
          </button>
        </div>
      )}
      <div className="file-panel admin-users">
        {users.map((u) => (
          <div className="file-row" key={u.id}>
            <span className="avatar">{u.email[0].toUpperCase()}</span>
            <div>
              <strong>{u.email}</strong>
              <small>
                {u.disabled ? "已禁用" : labels[u.state]}
                {u.admin ? " · 管理员" : ""}
              </small>
            </div>
            <button
              disabled={busy}
              onClick={() =>
                void run(async () => {
                  await request(`/api/v1/admin/users/${u.id}/stop`, "POST", {});
                  await refresh();
                })
              }
            >
              停止
            </button>
            {!u.admin && (
              <button
                disabled={busy}
                onClick={() =>
                  void run(async () => {
                    await request(
                      `/api/v1/admin/users/${u.id}/${u.disabled ? "enable" : "disable"}`,
                      "POST",
                      {},
                    );
                    await refresh();
                  })
                }
              >
                {u.disabled ? "启用" : "禁用"}
              </button>
            )}
          </div>
        ))}
      </div>
    </>
  );
}
createRoot(document.getElementById("root")!).render(<App />);
