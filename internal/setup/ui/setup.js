const tokenKey = "cloud-browser-setup-token";
const hash = new URLSearchParams(location.hash.slice(1));
if (hash.get("token")) sessionStorage.setItem(tokenKey, hash.get("token"));
if (location.hash) history.replaceState(null, "", location.pathname);
const token = sessionStorage.getItem(tokenKey) || "";
const form = document.querySelector("#wizard");
const fields = [...document.querySelectorAll("fieldset")];
const stepItems = [...document.querySelectorAll("#steps li")];
const existingDatabase = document.querySelector("#existing-database");
const databaseControls = [...existingDatabase.querySelectorAll("input,select")];
let step = 0;

const headers = () => ({
  Authorization: `Bearer ${token}`,
  "Content-Type": "application/json",
});
if (!token) document.querySelector("#token-error").classList.remove("hidden");

function showStep(next) {
  step = Math.max(0, Math.min(fields.length - 1, next));
  fields.forEach((field, index) => (field.hidden = index !== step));
  stepItems.forEach((item, index) =>
    item.classList.toggle("active", index === step),
  );
  document.querySelector("#counter").textContent = `第 ${step + 1} 步，共 4 步`;
  document.querySelector("#progress").style.width = `${(step + 1) * 25}%`;
  document.querySelector("#back").hidden = step === 0;
  document.querySelector("#next").hidden = step === fields.length - 1;
  document.querySelector("#install").hidden = step !== fields.length - 1;
  if (step === 3) renderSummary();
}

function validateStep() {
  const controls = [...fields[step].querySelectorAll("input,select")].filter(
    (control) => !control.disabled,
  );
  for (const control of controls) if (!control.reportValidity()) return false;
  if (
    step === 2 &&
    form.adminPassword.value !== form.adminPasswordConfirm.value
  ) {
    form.adminPasswordConfirm.setCustomValidity("两次输入的密码不一致");
    form.adminPasswordConfirm.reportValidity();
    return false;
  }
  form.adminPasswordConfirm.setCustomValidity("");
  return true;
}

function databaseLabel() {
  if (form.databaseMode.value === "internal") return "内置 PostgreSQL";
  if (form.databaseMode.value === "local")
    return `本机 PostgreSQL · ${form.databasePort.value}`;
  return `${form.databaseHost.value}:${form.databasePort.value}`;
}

function renderSummary() {
  const gateway =
    form.gatewayMode.value === "direct" ? "自动 HTTPS" : "已有反向代理";
  document.querySelector("#summary").innerHTML =
    `<div><span>数据库</span><strong>${escapeHTML(databaseLabel())}</strong></div><div><span>访问地址</span><strong>https://${escapeHTML(form.domain.value)}</strong></div><div><span>接入方式</span><strong>${gateway}</strong></div><div><span>管理员</span><strong>${escapeHTML(form.adminEmail.value)}</strong></div><div><span>并发会话</span><strong>${form.maxSessions.value} 个</strong></div>`;
}

function escapeHTML(value) {
  const node = document.createElement("span");
  node.textContent = value;
  return node.innerHTML;
}

function payload() {
  const value = Object.fromEntries(new FormData(form));
  delete value.adminPasswordConfirm;
  delete value.confirmed;
  value.databasePort = Number(value.databasePort || 5432);
  value.maxSessions = Number(value.maxSessions || 1);
  value.databaseName ||= "cloudbrowser";
  value.databaseUser ||= "cloudbrowser";
  value.databaseSSLMode ||= "disable";
  value.databaseHost ||= "";
  value.databasePassword ||= "";
  value.domain ||= "browser.example.com";
  value.gatewayMode ||= "direct";
  value.adminEmail ||= "admin@example.com";
  value.adminPassword ||= "not-used-for-database-test";
  return value;
}

function updateDatabaseMode() {
  const mode = form.databaseMode.value;
  const existing = mode !== "internal";
  existingDatabase.classList.toggle("hidden", !existing);
  document
    .querySelector("#remote-host")
    .classList.toggle("hidden", mode !== "remote");
  document
    .querySelector("#local-help")
    .classList.toggle("hidden", mode !== "local");
  databaseControls.forEach((control) => {
    control.disabled = !existing;
    if (
      existing &&
      ["databaseName", "databaseUser", "databasePassword"].includes(
        control.name,
      )
    ) {
      control.required = true;
    }
  });
  form.databaseHost.required = mode === "remote";
  form.databaseSSLMode.value = mode === "remote" ? "require" : "disable";
  document.querySelector("#database-test-result").textContent = "";
}

document.querySelector("#next").onclick = () => {
  if (validateStep()) showStep(step + 1);
};
document.querySelector("#back").onclick = () => showStep(step - 1);
form.databaseMode.forEach((control) => (control.onchange = updateDatabaseMode));

document.querySelector("#test-database").onclick = async () => {
  if (!validateStep() || !token) return;
  const button = document.querySelector("#test-database");
  const result = document.querySelector("#database-test-result");
  button.disabled = true;
  result.className = "testing";
  result.textContent = "正在从应用容器网络测试…";
  try {
    const response = await fetch("/api/setup/database/test", {
      method: "POST",
      headers: headers(),
      body: JSON.stringify(payload()),
    });
    const body = await response.json();
    if (!response.ok) throw new Error(body.message || body.error);
    result.className = "success";
    result.textContent = "✓ " + body.message;
  } catch (error) {
    result.className = "failure";
    result.textContent = "连接失败：" + error.message;
  } finally {
    button.disabled = false;
  }
};

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!validateStep() || !token) return;
  form.classList.add("hidden");
  document.querySelector("#installing").classList.remove("hidden");
  try {
    const response = await fetch("/api/setup/complete", {
      method: "POST",
      headers: headers(),
      body: JSON.stringify(payload()),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.message || result.error);
    poll();
  } catch (error) {
    failed(error.message);
  }
});

async function poll() {
  try {
    const response = await fetch("/api/setup/status", { headers: headers() });
    const status = await response.json();
    if (!response.ok) throw new Error(status.error);
    renderStatus(status);
    if (status.state === "COMPLETE") {
      const link = document.querySelector("#open-app");
      link.href = status.origin;
      link.classList.remove("hidden");
      sessionStorage.removeItem(tokenKey);
      return;
    }
    if (status.state === "FAILED") return failed(status.message);
    setTimeout(poll, 1500);
  } catch (error) {
    failed(error.message);
  }
}

function renderStatus(status) {
  document.querySelector("#install-title").textContent = status.step;
  document.querySelector("#install-message").textContent = status.message;
  document.querySelector(".install-progress i").style.width =
    `${status.progress}%`;
}

async function resume() {
  if (!token) return;
  try {
    const response = await fetch("/api/setup/status", { headers: headers() });
    const status = await response.json();
    if (!response.ok) throw new Error(status.error);
    if (status.state === "READY") return;
    form.classList.add("hidden");
    document.querySelector("#installing").classList.remove("hidden");
    renderStatus(status);
    if (status.state === "FAILED") return failed(status.message);
    if (status.state === "COMPLETE") {
      const link = document.querySelector("#open-app");
      link.href = status.origin;
      link.classList.remove("hidden");
      sessionStorage.removeItem(tokenKey);
      return;
    }
    poll();
  } catch (error) {
    document.querySelector("#token-error").textContent =
      "无法验证安装状态：" + error.message;
    document.querySelector("#token-error").classList.remove("hidden");
  }
}

async function detectEnvironment() {
  if (!token) return;
  try {
    const response = await fetch("/api/setup/environment", {
      headers: headers(),
    });
    const environment = await response.json();
    if (response.ok && environment.localPostgresDetected)
      document.querySelector("#local-detected").classList.remove("hidden");
  } catch (_) {
    // Detection is only a hint; database connection testing remains authoritative.
  }
}

function failed(message) {
  document.querySelector("#install-title").textContent = "安装未完成";
  document.querySelector("#install-message").textContent = message;
  document.querySelector("#retry").classList.remove("hidden");
}

document.querySelector("#retry").onclick = () => {
  document.querySelector("#installing").classList.add("hidden");
  document.querySelector("#retry").classList.add("hidden");
  form.classList.remove("hidden");
  showStep(0);
};

updateDatabaseMode();
showStep(0);
detectEnvironment();
resume();
