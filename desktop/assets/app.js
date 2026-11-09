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
// Run-mode form (local Hub / remote Hub) has its own dirty flag: the radios and
// the remote address are saved by their own button and edited independently of
// the network binding form below. runModeDirty keeps a 3s status refresh from
// overwriting text the user is still typing.
let runMode = "local";
let runModeDirty = false;
// restartBannerShown is memory-only: it only appears when a save response says
// restart_required, which is now the legacy case (a file saved by an older build
// that never applied it). ApplyMode switches at runtime, so the normal path
// reports restart_required=false; a status refresh never re-shows the banner.
let restartBannerShown = false;
// switchingMode is true while ApplyMode is in flight: the backend is stopping or
// starting the Hub, so the periodic refresh must not repaint half-switched state
// or overwrite the form the user just submitted.
let switchingMode = false;
let mode = "single";
let chosenAddress = null;
let dualLAN = null;
let dualTS = null;
// Pairing display state. The code is kept only in memory for as long as it is
// valid on screen: it is never stored in localStorage, a cookie, a log or the
// desktop config file. codeExpiresAt is the Hub-provided Unix-millisecond
// deadline; 0 means "no code shown".
let pairingTimer = null;
let codeExpiresAt = 0;
// generatingCode keeps the periodic refresh from re-enabling the button while a
// code request is still in flight (which would allow a double generation).
let generatingCode = false;
// hubKey is the running Hub (or "" when stopped). A change means the Hub was
// restarted or switched address, which invalidates any displayed code.
let hubKey = null;

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

// --- run mode (local Hub / remote Hub) ------------------------------------

// appliedRemote is the *saved* mode reported by the Hub, and it alone decides
// which blocks are visible: flipping the radio before saving must not hide the
// local blocks or make the page look like the mode already took effect.
function appliedRemote() {
  return !!state && state.mode === "remote";
}

function syncRunMode(s) {
  if (runModeDirty) return;
  runMode = s.mode === "remote" ? "remote" : "local";
  const input = $("remote-url");
  if (input && document.activeElement !== input) {
    input.value = s.remote_url || "";
  }
}

function renderRunMode() {
  const s = state;
  if (!s) return;
  $("run-local").checked = runMode !== "remote";
  $("run-remote").checked = runMode === "remote";
  $("remote-pane").hidden = runMode !== "remote";

  // Remote mode: hide the local-Hub-only blocks instead of removing them, so
  // switching back restores exactly the same DOM and layout. The elements keep
  // being rendered/updated; only their visibility changes.
  const remote = s.mode === "remote";
  $("network-card").hidden = remote;
  $("pairing-card").hidden = remote;
  $("remote-status-card").hidden = !remote;
  show($("remote-current"), remote ? "当前：远程 Hub · " + (s.remote_url || "（未填写地址）") : "");

  // Driven only by the last successful save; refresh() must not re-show it.
  $("run-mode-restart").hidden = !restartBannerShown;
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
  syncRunMode(s);

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
  $("tray-state").textContent = s.tray_available
    ? "可用：关闭窗口会收进托盘，Hub 继续运行"
    : "不可用：关闭窗口会询问是否退出（不会留后台进程）";
  $("cfg-path").textContent = s.config_path ? "桌面设置：" + s.config_path : "";
  // This window is itself a paired device: it logs in with its own device
  // token, so it gets one informational row without a revoke button. An empty
  // value means remote mode or "not paired yet".
  show(
    $("local-device"),
    s.local_device_name
      ? "本机窗口：" + s.local_device_name + "（免码登录，不可撤销；撤销后会在下次启动时重新配对）"
      : ""
  );

  // A stopped Hub, a restarted Hub or a new address makes any code on screen
  // stale: clear it instead of leaving an old code looking like it still works.
  const currentHub = s.running ? (s.url || "") : "";
  if (hubKey !== null && currentHub !== hubKey) {
    clearPairingCode();
  }
  hubKey = currentHub;
  // A code from local mode is meaningless once the window is in remote mode.
  if (s.mode === "remote") clearPairingCode();

  show($("startup-error"), s.startup_error || "");
  show($("proxy-warning"), s.proxy_warning || "");
  show($("a11y-warning"), s.accessibility_warning || "");
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
  renderRunMode();
  renderURLs();
}

// --- device pairing --------------------------------------------------------

// clearPairingCode removes the displayed code and stops its countdown. It runs
// when the code expires, when the Hub restarts or switches address, when the
// window is closed and whenever a generation attempt starts or fails, so an
// invalid code is never left on screen as if it still worked.
function clearPairingCode() {
  if (pairingTimer) {
    clearInterval(pairingTimer);
    pairingTimer = null;
  }
  codeExpiresAt = 0;
  const box = $("pairing-code");
  if (box) box.hidden = true;
  const value = $("pairing-code-value");
  if (value) value.textContent = "";
  const expiry = $("pairing-code-expiry");
  if (expiry) {
    expiry.textContent = "";
    expiry.hidden = true;
  }
  // The success banner belongs to the displayed code only: generating a new
  // code, a Hub restart or closing the window must not leave it behind.
  show($("pairing-status"), "");
}

function tickPairingCode() {
  if (!codeExpiresAt) return;
  const remaining = Math.round((codeExpiresAt - Date.now()) / 1000);
  if (remaining <= 0) {
    clearPairingCode();
    show($("pairing-error"), "配对码已过期，请重新生成。");
    return;
  }
  show($("pairing-code-expiry"), `剩余 ${remaining} 秒；过期后请重新生成。`);
}

// showPairingCode renders the code as text (never HTML) with its countdown.
// The Hub sends the code as a string, so a leading zero is kept.
function showPairingCode(codeValue, expiresAt) {
  clearPairingCode();
  const text = String(codeValue || "");
  if (!/^[0-9]{4}$/.test(text)) {
    show($("pairing-error"), "生成配对码失败：Hub 返回的不是有效的四位码。");
    return;
  }
  codeExpiresAt = Number(expiresAt) || 0;
  $("pairing-code-value").textContent = text;
  $("pairing-code").hidden = false;
  tickPairingCode();
  pairingTimer = setInterval(tickPairingCode, 500);
}

function formatTime(ms) {
  const n = Number(ms);
  if (!n || !Number.isFinite(n)) return "—";
  try {
    return new Date(n).toLocaleString("zh-CN", { hour12: false });
  } catch {
    return "—";
  }
}

// renderDevices builds the device list with textContent only: a device name is
// remote input and must never be parsed as HTML.
function renderDevices(devices) {
  const list = $("device-list");
  list.textContent = "";
  const items = devices || [];
  for (const dev of items) {
    const li = document.createElement("li");
    li.className = "device-item";
    const body = document.createElement("div");
    body.className = "device-body";
    const name = document.createElement("span");
    name.className = "device-name";
    name.textContent = dev.name || "未命名设备";
    const meta = document.createElement("span");
    meta.className = "device-meta";
    meta.textContent = "创建 " + formatTime(dev.created_at) + " · 最近使用 " + formatTime(dev.last_seen_at);
    body.appendChild(name);
    body.appendChild(meta);
    const revoke = document.createElement("button");
    revoke.type = "button";
    revoke.className = "ghost danger";
    revoke.textContent = "解除配对";
    revoke.addEventListener("click", () => revokeDevice(dev.id, revoke));
    li.appendChild(body);
    li.appendChild(revoke);
    list.appendChild(li);
  }
  show($("device-empty"), items.length ? "" : "还没有已配对设备；点「添加设备」生成四位码。");
}

async function revokeDevice(id, btn) {
  if (!id) return;
  show($("device-error"), "");
  btn.disabled = true;
  btn.textContent = "正在解除…";
  try {
    await call("RevokePairedDevice", id);
    await refreshDevices();
  } catch (err) {
    show($("device-error"), "解除配对失败：" + String(err));
    btn.disabled = false;
    btn.textContent = "解除配对";
  }
}

// refreshPairingCodeState hides a displayed code once the local Hub reports
// that no usable code is left, which is what happens when a phone consumes it:
// the countdown alone would keep the dead code on screen for up to 120s. Only a
// boolean crosses the bridge, so the code is never re-read, logged or stored.
// The 3s refresh interval bounds the delay well under the 3s requirement.
async function refreshPairingCodeState() {
  if (!codeExpiresAt) return;
  let active;
  try {
    active = await call("PairingCodeActive");
  } catch {
    // A Hub that just stopped is reported by render()/refreshDevices().
    return;
  }
  if (active || !codeExpiresAt) return;
  const remaining = Math.round((codeExpiresAt - Date.now()) / 1000);
  // An expired code keeps its own "过期" message from tickPairingCode.
  if (remaining <= 0) return;
  clearPairingCode();
  show($("pairing-status"), "配对成功，配对码已使用并自动隐藏。");
}

// refreshDevices loads the paired device list, or explains why pairing is
// unavailable. Its errors live in the pairing card and never touch the network
// settings above.
async function refreshDevices() {
  const add = $("pair-add");
  if (!state || !state.running) {
    show($("pairing-unavailable"), "Hub 未运行：现在无法配对设备。请先在下方「网络绑定」中保存地址并启动 Hub。");
    add.disabled = true;
    show($("device-error"), "");
    clearPairingCode();
    renderDevices([]);
    show($("device-empty"), "");
    return;
  }
  show($("pairing-unavailable"), "");
  add.disabled = generatingCode;
  try {
    renderDevices(await call("ListPairedDevices"));
    show($("device-error"), "");
  } catch (err) {
    show($("device-error"), "读取已配对设备失败：" + String(err));
  }
}

async function refresh() {
  // A switch in flight is stopping/starting the Hub: its intermediate state is
  // not worth painting and would fight the response that is on its way.
  if (switchingMode) return;
  try {
    render(await call("GetState"));
    // In remote mode the local-Hub-only bindings (ListPairedDevices,
    // PairingCodeActive) answer with a Chinese error by design. Skip them there
    // so the 3s refresh does not paint an error every time.
    if (appliedRemote()) return;
    await refreshDevices();
    await refreshPairingCodeState();
  } catch (err) {
    show($("apply-error"), "读取状态失败：" + err);
  }
}

$("run-local").addEventListener("change", () => {
  runMode = "local";
  runModeDirty = true;
  renderRunMode();
});
$("run-remote").addEventListener("change", () => {
  runMode = "remote";
  runModeDirty = true;
  renderRunMode();
});
$("remote-url").addEventListener("input", () => {
  runModeDirty = true;
});

// Saving the mode calls ApplyMode(mode, url). The backend validates the address
// and switches the running process immediately (stops/starts the Hub, reloads
// the inbox window), so the response already describes the new mode. A readable
// Chinese error is shown in this block's own error line (never an alert), and
// restart_required in the response is only the legacy "saved but not applied"
// fallback banner.
$("run-mode-save").addEventListener("click", async () => {
  show($("run-mode-error"), "");
  show($("run-mode-ok"), "");
  const target = $("run-remote").checked ? "remote" : "local";
  const url = ($("remote-url").value || "").trim();
  if (target === "remote") {
    if (!url) {
      show($("run-mode-error"), "请填写远程 Hub 地址。");
      return;
    }
    if (!/^https?:\/\//i.test(url)) {
      show($("run-mode-error"), "远程 Hub 地址必须以 http:// 或 https:// 开头。");
      return;
    }
  }
  const btn = $("run-mode-save");
  switchingMode = true;
  btn.disabled = true;
  btn.textContent = "正在切换…";
  try {
    const next = await call("ApplyMode", target, target === "remote" ? url : "");
    runModeDirty = false;
    restartBannerShown = !!(next && next.restart_required);
    if (restartBannerShown) {
      $("run-mode-restart").textContent =
        "已保存，请从托盘退出 FlowDrop 后重新启动，新模式才会生效。";
    }
    render(next);
    if (!restartBannerShown) {
      show($("run-mode-ok"), target === "remote"
        ? "已切换到远程 Hub：本机 Hub 已停止，收件箱指向 " + (next.remote_url || "") + "。"
        : "已切换到本机 Hub：Hub 已启动，收件箱指向 " + (next.url ? next.url + "/" : "本机 Hub") + "。");
    }
    if (appliedRemote()) {
      clearPairingCode();
    } else {
      await refreshDevices();
    }
  } catch (err) {
    // Keep runModeDirty so the next status refresh does not overwrite what the
    // user typed or which radio they picked before retrying.
    show($("run-mode-error"), String(err));
  } finally {
    switchingMode = false;
    btn.disabled = false;
    btn.textContent = "保存";
  }
});

$("remote-open-inbox").addEventListener("click", () => {
  call("OpenInbox").catch((err) => show($("run-mode-error"), String(err)));
});

$("remote-open-browser").addEventListener("click", async () => {
  try {
    await call("OpenInBrowser");
  } catch (err) {
    show($("run-mode-error"), String(err));
  }
});

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
    // The restart replaced the Hub instance, so a code shown before is dead.
    clearPairingCode();
    await refreshDevices();
  } catch (err) {
    show($("apply-error"), String(err));
    dirty = false;
    await refresh();
  } finally {
    btn.disabled = false;
    btn.textContent = "保存并重启 Hub";
  }
});

// "添加设备" asks the local Hub for a fresh code. Generation starts by
// clearing any previously shown code, so a failure can never leave the old one
// on screen looking valid; the code itself stays in memory only.
$("pair-add").addEventListener("click", async () => {
  const btn = $("pair-add");
  show($("pairing-error"), "");
  clearPairingCode();
  generatingCode = true;
  btn.disabled = true;
  btn.textContent = "正在生成…";
  try {
    const code = await call("CreatePairingCode");
    showPairingCode(code && code.code, code && code.expires_at);
  } catch (err) {
    show($("pairing-error"), "生成配对码失败：" + String(err));
  } finally {
    generatingCode = false;
    btn.disabled = !state || !state.running;
    btn.textContent = "添加设备";
  }
});

// Closing the settings window must not leave a code behind in the UI or in a
// timer; nothing about it is persisted anywhere.
window.addEventListener("pagehide", clearPairingCode);
window.addEventListener("beforeunload", clearPairingCode);

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

// Window chrome: the settings window is frameless, so its title bar sends the
// three fixed window operations the shell accepts (window_chrome.go). They are
// raw messages, not service calls: the page cannot name any other native action.
for (const [id, action] of [
  ["tb-min", "minimise"],
  ["tb-max", "maximise"],
  ["tb-close", "close"],
]) {
  const el = document.getElementById(id);
  if (!el) {
    continue;
  }
  el.addEventListener("click", (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    try {
      window._wails.invoke(`flowdrop:window:${action}`);
    } catch {
      /* the chrome must never throw into the settings page */
    }
  });
}
