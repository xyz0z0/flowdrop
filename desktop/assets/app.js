// Settings window logic. This document is served by the in-process Wails asset
// server (wails://localhost), so it can call the bound Go service. The inbox
// window loads the Hub's own HTTP origin and has no access to these methods.

import { Call } from "/wails/runtime.js";

const SERVICE = "main.SettingsService";
const call = (name, ...args) => Call.ByName(`${SERVICE}.${name}`, ...args);

const $ = (id) => document.getElementById(id);
let state = null;
// dirty means the user changed something that has not been saved yet; periodic
// status refreshes must not overwrite those inputs.
let dirty = false;
let mode = "single";
let chosenAddress = null;
let dualLAN = null;
let dualTS = null;

function show(el, text) {
  if (!el) return;
  if (text) {
    el.textContent = text;
    el.hidden = false;
  } else {
    el.hidden = true;
    el.textContent = "";
  }
}

function kindLabel(kind) {
  switch (kind) {
    case "lan":
      return "内网 LAN";
    case "tailscale":
      return "Tailscale";
    case "loopback":
      return "本机回环";
    case "virtual":
      return "虚拟/容器网卡";
    default:
      return "其他地址";
  }
}

function byKind(kind) {
  return (state.interfaces || []).filter((i) => i.kind === kind);
}

function option(select, value, label, selected) {
  const opt = document.createElement("option");
  opt.value = value;
  opt.textContent = label;
  opt.selected = selected;
  select.appendChild(opt);
}

// fillSelect rebuilds one address select. A saved address that is no longer on
// this machine stays visible (labelled as missing) so the failing configuration
// is honest; saving it again will produce a clear error instead of silent
// fallback.
function fillSelect(select, kind, saved) {
  select.innerHTML = "";
  const list = byKind(kind);
  for (const iface of list) {
    option(select, iface.address, `${iface.label} — ${iface.address}`, iface.address === saved);
  }
  if (saved && !list.some((i) => i.address === saved)) {
    option(select, saved, `${saved}（当前网卡列表中不存在）`, true);
  }
  if (!saved && list.length > 0) {
    select.value = list[0].address;
  }
  if (list.length === 0 && !saved) {
    option(select, "", "没有可用地址", true);
  }
}

function syncControls(s) {
  if (dirty) return;
  if (s.dual_mode) {
    mode = "dual";
  } else if (mode !== "dual" || !s.dual_available) {
    mode = "single";
  }
  chosenAddress = s.bind_address || (s.interfaces[0] && s.interfaces[0].address) || "127.0.0.1";
  const lanList = byKind("lan");
  const tsList = byKind("tailscale");
  dualLAN = s.bind_address && lanList.some((i) => i.address === s.bind_address)
    ? s.bind_address
    : (lanList[0] && lanList[0].address) || "";
  dualTS = (s.additional_addresses && s.additional_addresses[0])
    || (tsList[0] && tsList[0].address) || "";
  $("port").value = s.port || 8080;
}

function renderMode() {
  const s = state;
  if (!s) return;
  const dualAvailable = !!s.dual_available;
  const dualRadio = $("mode-dual");
  dualRadio.checked = mode === "dual";
  $("mode-single").checked = mode === "single";
  // A saved dual configuration stays visible even if Tailscale is currently
  // gone, but selecting dual anew requires both address kinds.
  dualRadio.disabled = !dualAvailable && mode !== "dual";
  $("dual-pane").hidden = mode !== "dual";
  $("single-pane").hidden = mode === "dual";

  let hint = "";
  if (mode === "dual" && !dualAvailable) {
    hint = "已保存的双模式配置需要 Tailscale 地址，但当前没有检测到 Tailscale 网卡。"
      + "直接保存会失败；请接入 Tailscale 后重试，或改为单地址模式。";
  } else if (!dualAvailable) {
    hint = "未检测到 Tailscale 网卡，不能选择「同时使用内网和 Tailscale」；内网单地址模式仍可用。"
      + "本应用不会要求或自动安装 Tailscale。";
  }
  show($("dual-hint"), hint);
  $("tailscale-hint").hidden = s.has_tailscale || mode === "dual";
}

function renderURLs() {
  const s = state;
  const list = $("url-list");
  list.innerHTML = "";
  const urls = s.access_urls || [];
  for (const entry of urls) {
    const li = document.createElement("li");
    li.className = "url-item";
    const code = document.createElement("code");
    code.textContent = entry.url + "/";
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = entry.primary ? "主地址（收件箱默认）" : kindLabel(entry.kind);
    const label = document.createElement("span");
    label.className = "url-label";
    label.textContent = entry.label || "";
    li.appendChild(code);
    li.appendChild(tag);
    li.appendChild(label);
    list.appendChild(li);
  }
  show($("url-empty"), s.running && urls.length > 0 ? "" : "Hub 未运行：上方显示的是已保存的设置，不是当前可用地址。");
}

function render(s) {
  state = s;
  syncControls(s);

  const badge = $("status-badge");
  badge.textContent = s.running ? "运行中" : "未运行";
  badge.className = "badge " + (s.running ? "badge-on" : "badge-off");

  $("hub-url").textContent = s.url ? s.url + "/" : "—";
  const listens = [];
  if (s.bind_address) listens.push(`${s.bind_address}:${s.port || "?"}`);
  for (const addr of s.additional_addresses || []) {
    listens.push(`${addr}:${s.port || "?"}`);
  }
  // Only call it "actual" when the Hub is really running; otherwise the same
  // values are just the saved request shown in the inputs below.
  $("listen").textContent = !s.running
    ? "—（未运行，当前没有监听）"
    : (listens.length ? listens.join("  +  ") : "—");
  $("admin-state").textContent = s.admin_configured
    ? "已设置（密码登录仍由 Hub 校验）"
    : "尚未设置";
  $("tray-state").textContent = s.tray_available
    ? "可用：关闭窗口会收进托盘，Hub 继续运行"
    : "不可用：关闭窗口会询问是否退出（不会留后台进程）";
  $("cfg-path").textContent = s.config_path ? "桌面设置：" + s.config_path : "";
  $("admin-card").hidden = s.admin_configured;

  show($("startup-error"), s.startup_error || "");
  show($("proxy-warning"), s.proxy_warning || "");
  show($("last-error"), s.startup_error ? "" : s.last_error || "");
  $("tailscale-hint").hidden = s.has_tailscale || mode === "dual";

  // Address lists.
  const list = $("iface-list");
  list.innerHTML = "";
  if (!s.interfaces || s.interfaces.length === 0) {
    const p = document.createElement("p");
    p.className = "hint hint-warn";
    p.textContent = "没有检测到可用的 IPv4 地址。";
    list.appendChild(p);
  } else {
    for (const iface of s.interfaces) {
      const label = document.createElement("label");
      label.className = "iface" + (iface.kind === "loopback" || iface.kind === "virtual" ? " iface-muted" : "");
      const radio = document.createElement("input");
      radio.type = "radio";
      radio.name = "iface";
      radio.value = iface.address;
      radio.checked = iface.address === chosenAddress;
      radio.addEventListener("change", () => {
        chosenAddress = iface.address;
        dirty = true;
      });
      const body = document.createElement("span");
      body.className = "iface-body";
      const title = document.createElement("span");
      title.className = "iface-label";
      title.textContent = iface.label;
      const addr = document.createElement("code");
      addr.textContent = iface.address;
      body.appendChild(title);
      body.appendChild(addr);
      if (iface.recommended) {
        const tag = document.createElement("em");
        tag.className = "tag";
        tag.textContent = "推荐";
        body.appendChild(tag);
      }
      label.appendChild(radio);
      label.appendChild(body);
      list.appendChild(label);
    }
  }

  fillSelect($("dual-lan"), "lan", dualLAN);
  fillSelect($("dual-ts"), "tailscale", dualTS);
  dualLAN = $("dual-lan").value;
  dualTS = $("dual-ts").value;

  renderMode();
  renderURLs();
}

async function refresh() {
  try {
    render(await call("GetState"));
  } catch (err) {
    show($("apply-error"), "读取状态失败：" + err);
  }
}

$("mode-single").addEventListener("change", () => {
  mode = "single";
  dirty = true;
  renderMode();
});
$("mode-dual").addEventListener("change", () => {
  mode = "dual";
  dirty = true;
  renderMode();
});
$("dual-lan").addEventListener("change", () => {
  dualLAN = $("dual-lan").value;
  dirty = true;
});
$("dual-ts").addEventListener("change", () => {
  dualTS = $("dual-ts").value;
  dirty = true;
});
$("port").addEventListener("input", () => {
  dirty = true;
});

$("apply").addEventListener("click", async () => {
  show($("apply-error"), "");
  show($("apply-ok"), "");
  const port = Number($("port").value);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    show($("apply-error"), "端口需在 1-65535 之间。");
    return;
  }
  let callResult;
  const btn = $("apply");
  btn.disabled = true;
  btn.textContent = "正在重启 Hub…";
  try {
    if (mode === "dual") {
      const lan = $("dual-lan").value;
      const ts = $("dual-ts").value;
      if (!lan || !ts) {
        show($("apply-error"), "双模式需要同时选择一个内网地址和一个 Tailscale 地址。");
        return;
      }
      if (lan === ts) {
        show($("apply-error"), "内网地址和 Tailscale 地址不能相同。");
        return;
      }
      callResult = await call("ApplyDualNetwork", lan, ts, port);
    } else {
      if (!chosenAddress) {
        show($("apply-error"), "请选择一个绑定地址。");
        return;
      }
      callResult = await call("ApplyNetwork", chosenAddress, port);
    }
    dirty = false;
    render(callResult);
    const actual = (callResult.access_urls || []).map((u) => u.url + "/").join("  、  ");
    show($("apply-ok"), actual
      ? "已应用：Hub 已按新配置重启，实际绑定 " + actual
      : "已应用：Hub 已按新配置重启。");
  } catch (err) {
    show($("apply-error"), String(err));
    dirty = false;
    await refresh();
  } finally {
    btn.disabled = false;
    btn.textContent = "保存并重启 Hub";
  }
});

$("set-admin").addEventListener("click", async () => {
  show($("admin-error"), "");
  show($("admin-ok"), "");
  const user = $("admin-user").value.trim() || "admin";
  const pass = $("admin-pass").value;
  const pass2 = $("admin-pass2").value;
  if (pass.length < 10) {
    show($("admin-error"), "密码至少需要 10 个字符。");
    return;
  }
  if (pass !== pass2) {
    show($("admin-error"), "两次输入的密码不一致。");
    return;
  }
  const btn = $("set-admin");
  btn.disabled = true;
  try {
    render(await call("InitializeAdmin", user, pass));
    $("admin-pass").value = "";
    $("admin-pass2").value = "";
    show($("admin-ok"), "管理员密码已设置，可以登录收件箱了。");
  } catch (err) {
    show($("admin-error"), String(err));
  } finally {
    btn.disabled = false;
  }
});

$("open-inbox").addEventListener("click", () => {
  call("OpenInbox").catch((err) => show($("apply-error"), String(err)));
});

$("open-browser").addEventListener("click", async () => {
  try {
    await call("OpenInBrowser");
  } catch (err) {
    show($("last-error"), String(err));
  }
});

$("copy-url").addEventListener("click", async () => {
  const value = $("hub-url").textContent;
  try {
    await navigator.clipboard.writeText(value);
    $("copy-url").textContent = "已复制";
    setTimeout(() => ($("copy-url").textContent = "复制"), 1200);
  } catch {
    // Settings window is a secure context; if the platform still refuses,
    // select the text so the user can copy manually instead of lying.
    const range = document.createRange();
    range.selectNodeContents($("hub-url"));
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    $("copy-url").textContent = "请手动复制";
  }
});

$("quit").addEventListener("click", () => {
  call("Quit").catch(() => {});
});

refresh();
setInterval(refresh, 3000);
