"use strict";

// --- tiny fetch helper ---
async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch("/api" + path, opts);
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch (_) { data = { error: text }; }
  }
  if (!res.ok) {
    throw new Error((data && data.error) || ("HTTP " + res.status));
  }
  return data;
}

const $ = (id) => document.getElementById(id);

function setStatus(el, msg, kind) {
  el.textContent = msg || "";
  el.className = "status" + (kind ? " " + kind : "");
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// --- generic dialog (replaces native prompt / confirm / alert) ---
let dialogResolve = null;

function openDialog(opts) {
  return new Promise((resolve) => {
    dialogResolve = resolve;
    $("dialog-title").textContent = opts.title || "";
    const msg = $("dialog-msg");
    msg.textContent = opts.message || "";
    msg.classList.toggle("hidden", !opts.message);
    const input = $("dialog-input");
    if (opts.withInput) {
      input.classList.remove("hidden");
      input.value = opts.value || "";
    } else {
      input.classList.add("hidden");
    }
    const ok = $("dialog-ok");
    ok.textContent = opts.okText || "确定";
    ok.classList.toggle("danger", !!opts.danger);
    $("dialog-cancel").classList.toggle("hidden", !!opts.hideCancel);
    $("dialog").classList.add("open");
    setTimeout(() => {
      if (opts.withInput) { input.focus(); input.select(); } else ok.focus();
    }, 30);
  });
}

function dialogHasInput() { return !$("dialog-input").classList.contains("hidden"); }

function closeDialog(result) {
  $("dialog").classList.remove("open");
  const r = dialogResolve;
  dialogResolve = null;
  if (r) r(result);
}

$("dialog-ok").onclick = () => closeDialog(dialogHasInput() ? $("dialog-input").value : true);
$("dialog-cancel").onclick = () => closeDialog(dialogHasInput() ? null : false);
$("dialog").addEventListener("click", (e) => {
  if (e.target === $("dialog")) closeDialog(dialogHasInput() ? null : false);
});
$("dialog-input").addEventListener("keydown", (e) => {
  if (e.key === "Enter") { e.preventDefault(); closeDialog($("dialog-input").value); }
});
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  if ($("dialog").classList.contains("open")) closeDialog(dialogHasInput() ? null : false);
  else if ($("login-modal").classList.contains("open")) attemptCloseLoginModal();
});

// returns the entered string, or null if cancelled
const showPrompt = (title, value, message) => openDialog({ title, message, withInput: true, value });
// returns true / false
const showConfirm = (title, message, o) => openDialog({ title, message, okText: o && o.okText, danger: o && o.danger });
// info box with a single OK button
const showAlert = (title, message) => openDialog({ title, message, hideCancel: true });

// --- tabs ---
document.querySelectorAll(".tab").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((b) => b.classList.remove("active"));
    document.querySelectorAll(".panel").forEach((p) => p.classList.remove("active"));
    btn.classList.add("active");
    $("tab-" + btn.dataset.tab).classList.add("active");
    refreshTasks(); // keep whichever view we switched to fresh
  });
});

// --- account state (the global "current account" drives uploads) ---
let accounts = [];
let currentNs = localStorage.getItem("tdl_current_ns") || null;

function findAccount(ns) {
  return accounts.find((a) => a.namespace === ns) || null;
}

function accTitle(acc) {
  return acc.alias || acc.namespace;
}

// the account's Telegram display name (nickname, else @username)
function selfName(self) {
  if (!self) return "";
  return [self.first_name, self.last_name].filter(Boolean).join(" ") || self.username || "";
}

// sub-line under the title: just the ID (the name lives in the title/alias)
function accSub(acc) {
  if (acc.self && acc.self.id) return "ID " + acc.self.id;
  return "未查看";
}

function setCurrent(ns) {
  currentNs = ns;
  if (ns) localStorage.setItem("tdl_current_ns", ns);
  else localStorage.removeItem("tdl_current_ns");
  renderCurrent();
  renderAccountMenu();
  // account drives the conversation list (cached per-namespace); reset selection
  selectedChat = null;
  renderWindow();
  loadChats(ns);
}

// reflect the current account in the header button
function renderCurrent() {
  const acc = currentNs ? findAccount(currentNs) : null;
  if (acc) {
    $("acc-title").textContent = accTitle(acc);
    $("acc-sub").textContent = accSub(acc);
  } else {
    $("acc-title").textContent = "未登录";
    $("acc-sub").textContent = "";
  }
}

async function loadAccounts() {
  try {
    accounts = await api("GET", "/accounts");
  } catch (_) {
    accounts = [];
  }
  // reconcile the current selection with what actually exists
  if (!currentNs || !findAccount(currentNs)) {
    setCurrent(accounts.length ? accounts[0].namespace : null);
  } else {
    renderCurrent();
    renderAccountMenu();
    loadChats(currentNs); // current account unchanged: still (re)load its conversations
  }
}

function renderAccountMenu() {
  const list = $("accounts-list");
  list.innerHTML = "";

  if (!accounts.length) {
    const li = document.createElement("li");
    li.className = "acc-empty";
    li.textContent = "还没有账号，点下面登录添加一个。";
    list.appendChild(li);
    return;
  }

  accounts.forEach((acc) => {
    const li = document.createElement("li");
    li.className = "acc-item" + (acc.namespace === currentNs ? " current" : "");
    // clicking anywhere on the row (except the action buttons) switches account
    li.onclick = () => { setCurrent(acc.namespace); closeAccountMenu(); };

    const main = document.createElement("div");
    main.className = "acc-item-main";
    const title = document.createElement("span");
    title.className = "acc-item-title";
    title.textContent = accTitle(acc);
    const sub = document.createElement("span");
    sub.className = "acc-item-sub";
    sub.textContent = accSub(acc);
    main.append(title, sub);

    const actions = document.createElement("div");
    actions.className = "acc-item-actions";
    actions.append(
      iconBtn("⟳", "查看 / 刷新身份", () => viewAccount(acc.namespace)),
      iconBtn("✎", "改名", () => renameAccount(acc.namespace)),
      iconBtn("🗑", "删除", () => deleteAccount(acc.namespace), true),
    );

    li.append(main, actions);
    list.appendChild(li);
  });
}

function iconBtn(label, title, onclick, danger) {
  const b = document.createElement("button");
  b.className = "icon-btn" + (danger ? " danger" : "");
  b.textContent = label;
  b.title = title;
  b.onclick = (e) => { e.stopPropagation(); onclick(); };
  return b;
}

// connect once to fetch + cache id/username/nickname, then refresh the list
async function viewAccount(ns) {
  if (ns === currentNs) $("acc-sub").textContent = "连接中…";
  try {
    await api("GET", `/accounts/${encodeURIComponent(ns)}/self`);
    await loadAccounts();
  } catch (e) {
    if (ns === currentNs) renderCurrent();
    showAlert("查看失败", e.message);
  }
}

async function renameAccount(ns) {
  const acc = findAccount(ns);
  const cur = acc ? (acc.alias || "") : "";
  const next = await showPrompt("给账号起个名字", cur, `留空则恢复为原名 ${ns}`);
  if (next === null) return; // cancelled
  try {
    await api("PATCH", `/accounts/${encodeURIComponent(ns)}/alias`, { alias: next });
    await loadAccounts();
  } catch (e) {
    showAlert("改名失败", e.message);
  }
}

async function deleteAccount(ns) {
  const acc = findAccount(ns);
  const name = acc ? accTitle(acc) : ns;
  const ok = await showConfirm(`删除账号「${name}」？`, "会清除本地登录数据（session），不可恢复。", { danger: true, okText: "删除" });
  if (!ok) return;
  try {
    await api("DELETE", `/accounts/${encodeURIComponent(ns)}`);
    if (ns === currentNs) currentNs = null; // loadAccounts will pick a new one
    await loadAccounts();
  } catch (e) {
    showAlert("删除失败", e.message);
  }
}

// --- account dropdown open/close ---
function openAccountMenu() {
  $("account").classList.add("open");
  $("account-btn").setAttribute("aria-expanded", "true");
}
function closeAccountMenu() {
  $("account").classList.remove("open");
  $("account-btn").setAttribute("aria-expanded", "false");
}
$("account-btn").onclick = (e) => {
  e.stopPropagation();
  if ($("account").classList.contains("open")) closeAccountMenu();
  else openAccountMenu();
};
// click outside closes the menu
document.addEventListener("click", (e) => {
  if (!$("account").contains(e.target)) closeAccountMenu();
});

// --- login modal (wizard) ---
let loginId = null;
let loginNs = null;
let pendingAlias = "";
let loginPoll = null;
// the stage we've already submitted input for ("need_code" / "need_password");
// while the backend is still verifying it stays on that stage, so we show a
// steady "校验中…" instead of re-printing the prompt over our "submitted" message.
let submittedFor = null;
let loginMode = "code"; // active login tab: "code" | "qr"

function openLoginModal() {
  closeAccountMenu();
  loginId = null;
  loginMode = "code";
  // reset wizard: code tab active, step 1 (phone) visible
  document.querySelectorAll(".login-tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === "code"));
  $("step-phone").classList.remove("hidden");
  $("step-qr").classList.add("hidden");
  $("step-code").classList.add("hidden");
  $("step-password").classList.add("hidden");
  $("login-qr").removeAttribute("src");
  $("login-name").value = "";
  $("login-phone").value = "";
  $("login-code").value = "";
  $("login-password").value = "";
  $("btn-login-start").disabled = false;
  submittedFor = null;
  setStatus($("login-status"), "");
  $("login-modal").classList.add("open");
  $("login-name").focus();
}
function closeLoginModal() {
  cancelQRLogin();
  stopLogin();
  $("login-modal").classList.remove("open");
}

// true once the user has started typing or a code login is underway
function loginInProgress() {
  return !!loginId
    || $("login-name").value.trim() !== ""
    || $("login-phone").value.trim() !== ""
    || $("login-code").value.trim() !== ""
    || $("login-password").value !== "";
}

// close, but confirm first if there's unfinished login progress (avoids
// losing a half-done login to a stray backdrop click / Esc)
async function attemptCloseLoginModal() {
  if (loginInProgress()) {
    const ok = await showConfirm("放弃这次登录？", "登录还没完成，关闭会中断当前流程。", { okText: "放弃", danger: true });
    if (!ok) return;
  }
  closeLoginModal();
}

$("btn-open-login").onclick = openLoginModal;
$("login-close").onclick = closeLoginModal; // the ✕ is an explicit close
$("login-modal").addEventListener("click", (e) => {
  if (e.target === $("login-modal")) attemptCloseLoginModal(); // backdrop: confirm if mid-login
});

function sanitizeNs(s) {
  return s.trim().replace(/[\/\\:*?"<>|]/g, "_");
}

async function startLogin() {
  const name = $("login-name").value.trim();
  const status = $("login-status");
  // the field shows a fixed "+" prefix; accept input with or without one and
  // always submit exactly one leading "+".
  const digits = $("login-phone").value.trim().replace(/^\++/, "").trim();
  if (!digits) { setStatus(status, "请填手机号", "err"); return; }
  const phone = "+" + digits;

  let ns = sanitizeNs(name);
  if (!ns) ns = "tg" + phone.replace(/\D/g, "");
  loginNs = ns;
  pendingAlias = name; // set as the display name on success (if provided)
  submittedFor = null;

  $("btn-login-start").disabled = true;
  setStatus(status, "正在发送验证码…", "info");
  try {
    const r = await api("POST", `/accounts/${encodeURIComponent(ns)}/login/start`, { phone });
    loginId = r.login_id;
    $("step-code").classList.remove("hidden");
    $("step-password").classList.add("hidden");
    pollLogin();
  } catch (e) {
    setStatus(status, e.message, "err");
    $("btn-login-start").disabled = false;
  }
}

// --- login tabs: code | qr ---
function setLoginTab(mode) {
  if (mode === loginMode) return;
  if (loginMode === "qr") cancelQRLogin(); // leaving QR: drop its temp ns if unfinished
  loginMode = mode;
  stopLogin();
  loginId = null;
  submittedFor = null;
  $("login-code").value = "";
  $("login-password").value = "";
  document.querySelectorAll(".login-tab").forEach((t) => t.classList.toggle("active", t.dataset.tab === mode));
  $("step-code").classList.add("hidden");
  $("step-password").classList.add("hidden");
  setStatus($("login-status"), "");
  if (mode === "code") {
    $("step-qr").classList.add("hidden");
    $("login-qr").removeAttribute("src");
    $("step-phone").classList.remove("hidden");
    $("btn-login-start").disabled = false;
  } else {
    $("step-phone").classList.add("hidden");
    $("step-qr").classList.remove("hidden");
    startQRLogin();
  }
}
document.querySelectorAll(".login-tab").forEach((t) =>
  t.addEventListener("click", () => setLoginTab(t.dataset.tab)));

// startQRLogin begins a QR login and polls for the QR image + result. No phone
// is needed; a blank name generates a namespace and the account is later titled
// with the Telegram nickname (same done-branch as code login).
async function startQRLogin() {
  const status = $("login-status");
  const name = $("login-name").value.trim();
  let ns = sanitizeNs(name);
  if (!ns) ns = "tgqr" + Date.now();
  loginNs = ns;
  pendingAlias = name; // display name on success (blank → TG nickname)
  submittedFor = null;
  setStatus(status, "正在生成二维码…", "info");
  try {
    const r = await api("POST", `/accounts/${encodeURIComponent(ns)}/login/qr/start`, {});
    loginId = r.login_id;
    pollLogin();
  } catch (e) {
    setStatus(status, e.message, "err");
  }
}

// Abandon an in-progress QR login so the backend drops its temporary namespace
// (gotd writes a session on connect, which would otherwise linger as a ghost
// account). No-op unless a QR login is mid-flight.
function cancelQRLogin() {
  if (loginId && loginMode === "qr") {
    const id = loginId;
    loginId = null;
    api("POST", `/login/${encodeURIComponent(id)}/cancel`, {}).catch(() => {});
  }
}

function pollLogin() {
  if (loginPoll) clearInterval(loginPoll);
  loginPoll = setInterval(checkLogin, 1500);
  checkLogin();
}

async function checkLogin() {
  if (!loginId) return;
  const status = $("login-status");
  try {
    const st = await api("GET", `/login/${loginId}/status`);
    switch (st.stage) {
      case "starting":
        setStatus(status, "正在连接 Telegram…", "info"); break;
      case "need_qr":
        $("step-qr").classList.remove("hidden");
        if (st.qr) $("login-qr").src = st.qr;
        setStatus(status, "请用 Telegram 手机 App 扫描二维码…", "info");
        break;
      case "need_code":
        $("step-code").classList.remove("hidden");
        $("step-password").classList.add("hidden");
        setStatus(status,
          submittedFor === "need_code" ? "验证码已提交，校验中…" : "已发送验证码，请在 Telegram 客户端查收并填入。",
          "info");
        break;
      case "need_password":
        $("step-password").classList.remove("hidden");
        setStatus(status,
          submittedFor === "need_password" ? "密码已提交，校验中…" : "需要两步验证密码（2FA）。",
          "info");
        break;
      case "done": {
        stopLogin();
        loginId = null; // finished — nothing left to cancel on close
        setStatus(status, `登录成功！ID ${st.self.id}${st.self.username ? " · @" + st.self.username : ""}`, "ok");
        // name the account: typed name if given, otherwise the Telegram nickname
        const alias = pendingAlias || selfName(st.self);
        if (alias) {
          try { await api("PATCH", `/accounts/${encodeURIComponent(loginNs)}/alias`, { alias }); } catch (_) { /* ignore */ }
        }
        setCurrent(loginNs);
        await loadAccounts();
        setTimeout(closeLoginModal, 1000);
        break;
      }
      case "error":
        stopLogin();
        setStatus(status, "登录失败：" + (st.error || "未知错误"), "err");
        $("btn-login-start").disabled = false;
        break;
    }
  } catch (e) {
    stopLogin();
    setStatus(status, e.message, "err");
    $("btn-login-start").disabled = false;
  }
}

function stopLogin() {
  if (loginPoll) clearInterval(loginPoll);
  loginPoll = null;
}

$("btn-login-start").onclick = startLogin;

$("btn-login-code").onclick = async () => {
  const code = $("login-code").value.trim();
  if (!code || !loginId) return;
  try {
    await api("POST", `/login/${loginId}/code`, { code });
    submittedFor = "need_code";
    setStatus($("login-status"), "验证码已提交，校验中…", "info");
  } catch (e) {
    setStatus($("login-status"), e.message, "err");
  }
};

$("btn-login-password").onclick = async () => {
  const password = $("login-password").value;
  if (!password || !loginId) return;
  try {
    await api("POST", `/login/${loginId}/password`, { password });
    submittedFor = "need_password";
    setStatus($("login-status"), "密码已提交，校验中…", "info");
  } catch (e) {
    setStatus($("login-status"), e.message, "err");
  }
};

// Enter in a field submits that step (previously click-only — easy to miss,
// especially on the 2FA password box).
$("login-phone").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); startLogin(); } });
$("login-code").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); $("btn-login-code").click(); } });
$("login-password").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); $("btn-login-password").click(); } });
$("login-name").addEventListener("keydown", (e) => { if (e.key === "Enter" && loginMode === "code") { e.preventDefault(); $("login-phone").focus(); } });

// ===================== chats console (Telegram-style) =====================
// State: the conversation list (cached, per namespace), the global task list,
// and which conversation window is open.
let chats = [];          // normal conversations from the /chats cache (Dialog[])
let chatFolders = [];    // Telegram custom folders from the /chats cache
let allTasks = [];       // global task list from /tasks (all accounts)
let selectedChat = null; // the open conversation, or null
let chatSearch = "";
let activeFolder = "all";

function splitExts(s) {
  return s.split(",").map((x) => x.trim()).filter(Boolean);
}

// --- colored-initials avatar: a stable random color per key, kept in localStorage ---
function avatarColor(key) {
  const k = "tdl_color:" + key;
  let c = localStorage.getItem(k);
  if (!c) {
    c = `hsl(${Math.floor(Math.random() * 360)}, 62%, 45%)`; // random hue, fixed s/l for contrast
    localStorage.setItem(k, c);
  }
  return c;
}
function initialOf(name) {
  const s = String(name || "").trim();
  if (!s) return "?";
  return [...s][0].toUpperCase(); // first code point (handles CJK / emoji)
}
function setAvatar(el, name, colorKey) {
  el.textContent = initialOf(name);
  el.style.background = avatarColor(colorKey);
}

// the display name of an account (alias > Telegram nickname > namespace)
function accNick(ns) {
  const a = findAccount(ns);
  if (!a) return ns;
  return a.alias || selfName(a.self) || ns;
}

// an account's own Telegram user id (0 if not cached yet — login/⟳ caches it)
function selfIdOf(ns) {
  const a = findAccount(ns);
  return a && a.self && a.self.id ? a.self.id : 0;
}

// --- formatting ---
function fmtSize(n) {
  if (n === -1) return "目录";
  if (n == null || n < 0) return "";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + " " + u[i];
}
const pad2 = (n) => (n < 10 ? "0" + n : "" + n);
const hm = (d) => d.getHours() + ":" + pad2(d.getMinutes()); // 3:55
function sameDay(a, b) {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}
// left-list time: today -> H:MM, else M月D日 (with year if different)
function fmtListTime(d) {
  const now = new Date();
  if (sameDay(d, now)) return hm(d);
  if (d.getFullYear() === now.getFullYear()) return (d.getMonth() + 1) + "月" + d.getDate() + "日";
  return d.getFullYear() + "/" + (d.getMonth() + 1) + "/" + d.getDate();
}
// window date divider: M月D日 (with year if different)
function fmtDateDivider(d) {
  const base = (d.getMonth() + 1) + "月" + d.getDate() + "日";
  return d.getFullYear() === new Date().getFullYear() ? base : (d.getFullYear() + "年" + base);
}

// --- conversation identity ---
// kinds: 'saved' (per-account Saved Messages), 'normal' (a cached dialog)
function chatKey(c) {
  if (c.kind === "saved") return "s:" + c.ns;
  return "c:" + c.id;
}
function chatColorKey(c) {
  if (c.kind === "saved") return "saved:" + c.ns;
  return "chat:" + c.id;
}
function chatPeerKind(c) {
  if (!c) return "";
  if (c.peer_kind) return c.peer_kind;
  switch (c.type) {
    case "private": return "user";
    case "channel": return "channel";
    case "group": return "chat";
    default: return "";
  }
}
function savedEntry() {
  return { kind: "saved", ns: currentNs, id: 0, type: "saved", visible_name: "Saved Messages" };
}
// does a task belong to conversation c?
function taskMatchesChat(t, c) {
  if (c.kind === "saved") return t.namespace === c.ns && t.target === "Saved Messages";
  // direct: the task was sent TO this conversation's peer — the sender's own
  // view, or a shared group/channel (same id for every member).
  if (String(t.chat_id) === String(c.id)) return true;
  // received (private only): the task was sent BY this conversation's peer (c.id)
  // TO the current account. A private send to me carries chat_id == my own user
  // id, so from my side it belongs to the conversation with the sender. This is
  // what makes A→B show up under B's conversation with A (and vice-versa).
  const myId = selfIdOf(currentNs);
  if (c.kind === "normal" && c.type === "private" && myId
    && t.namespace !== currentNs
    && String(t.chat_id) === String(myId)
    && String(selfIdOf(t.namespace)) === String(c.id)) return true;
  return false;
}
function tasksForChat(c) {
  return allTasks.filter((t) => taskMatchesChat(t, c));
}
function lastTaskForChat(c) {
  const ts = tasksForChat(c);
  if (!ts.length) return null;
  return ts.reduce((a, b) => (new Date(a.created_at) >= new Date(b.created_at) ? a : b));
}
function taskFileName(t) {
  if (t.files && t.files.length) return t.files[0].name + (t.files.length > 1 ? ` +${t.files.length - 1}` : "");
  const base = ((t.paths && t.paths[0]) || "").split(/[\\/]/).pop();
  return base || "(无文件)";
}
function typeLabel(c) {
  if (c.kind === "saved") return "收藏夹";
  switch (c.type) {
    case "private": return c.bot ? "机器人" : "私聊";
    case "channel": return "频道";
    case "group": return "群组";
    default: return "会话";
  }
}

function folderKey(f) {
  return "f:" + f.id;
}
function folderLabel(f) {
  return ((f.emoticon || "") + " " + (f.title || ("分组 " + f.id))).trim();
}
function setChatsResponse(data) {
  chats = ((data && data.dialogs) || []).map((d) => Object.assign({ kind: "normal" }, d));
  chatFolders = ((data && data.folders) || []).filter((f) => f && f.id != null);
  if (activeFolder !== "all" && !chatFolders.some((f) => folderKey(f) === activeFolder)) {
    activeFolder = "all";
  }
  listSig = null;
  folderSig = null;
}
function chatMatchesFolder(c) {
  if (activeFolder === "all") return true;
  if (!c || c.kind !== "normal") return false;
  const id = activeFolder.slice(2);
  return ((c.folder_ids || []).map(String)).includes(id);
}
function folderChatCount(folderID) {
  const id = String(folderID);
  return chats.filter((c) => ((c.folder_ids || []).map(String)).includes(id)).length;
}
function activeFolderObj() {
  if (activeFolder === "all") return null;
  const id = activeFolder.slice(2);
  return chatFolders.find((f) => String(f.id) === id) || null;
}
function activeFolderPinRank(c) {
  if (!c || c.kind !== "normal") return -1;
  const f = activeFolderObj();
  if (!f || !f.pinned_peers || !f.pinned_peers.length) return -1;
  const kind = chatPeerKind(c);
  for (let i = 0; i < f.pinned_peers.length; i++) {
    const p = f.pinned_peers[i];
    if (String(p.id) === String(c.id) && String(p.peer_kind || "") === kind) return i;
  }
  return -1;
}
function isChatPinnedInView(c) {
  if (!c || c.kind !== "normal") return false;
  return activeFolderPinRank(c) >= 0;
}

// --- load conversations (cached, per namespace) ---
async function loadChats(ns) {
  if (!ns) {
    chats = [];
    chatFolders = [];
    activeFolder = "all";
    listSig = null;
    folderSig = null;
    renderChatList();
    return;
  }
  try {
    const data = await api("GET", `/accounts/${encodeURIComponent(ns)}/chats`);
    setChatsResponse(data);
  } catch (_) {
    chats = [];
    chatFolders = [];
    activeFolder = "all";
    listSig = null;
    folderSig = null;
  }
  renderChatList();
}

// live refresh from Telegram (also caches peer access-hashes for id uploads)
async function refreshChats() {
  const ns = currentNs;
  if (!ns) { showAlert("还没有账号", "请先登录一个账号。"); return; }
  const btn = $("btn-refresh-chats");
  const label = btn.textContent;
  btn.disabled = true; btn.textContent = "拉取中…";
  try {
    const data = await api("GET", `/accounts/${encodeURIComponent(ns)}/chats?refresh=1`);
    setChatsResponse(data);
    renderChatList();
  } catch (e) {
    showAlert("加载会话失败", e.message);
  } finally {
    btn.disabled = false; btn.textContent = label;
  }
}
$("btn-refresh-chats").onclick = refreshChats;
$("cw-refresh").onclick = refreshChats;

// --- settings modal (server-global upload rate + proxy) ---

// proxy is stored as a URL ("socks5://host:port"); the UI splits it into a
// scheme radio + host + port. parseProxy fills the fields from a stored URL.
function parseProxy(url) {
  let scheme = "socks5", host = "", port = "";
  const m = /^(socks5|http)?:?\/?\/?([^:]*)(?::(\d+))?$/i.exec((url || "").trim());
  if (m) {
    if (m[1]) scheme = m[1].toLowerCase();
    host = m[2] || "";
    port = m[3] || "";
  }
  $("proxy-socks5").checked = scheme !== "http";
  $("proxy-http").checked = scheme === "http";
  $("set-proxy-host").value = host;
  $("set-proxy-port").value = port;
}

// buildProxy assembles the stored URL from the fields; empty host = direct ("").
function buildProxy() {
  const host = $("set-proxy-host").value.trim();
  if (!host) return "";
  const scheme = $("proxy-http").checked ? "http" : "socks5";
  const port = $("set-proxy-port").value.trim();
  return scheme + "://" + host + (port ? ":" + port : "");
}

async function openSettings() {
  setStatus($("settings-status"), "");
  $("set-rate").value = "";
  parseProxy("");
  $("settings-modal").classList.add("open");
  try {
    const s = await api("GET", "/settings");
    $("set-rate").value = (s && s.rate) || "";
    parseProxy((s && s.proxy) || "");
  } catch (e) {
    setStatus($("settings-status"), "读取设置失败：" + e.message, "err");
  }
  setTimeout(() => $("set-rate").focus(), 30);
}
function closeSettings() { $("settings-modal").classList.remove("open"); }

async function saveSettings() {
  const btn = $("settings-save");
  btn.disabled = true;
  setStatus($("settings-status"), "保存中…");
  try {
    const res = await api("PUT", "/settings", {
      rate: $("set-rate").value.trim(),
      proxy: buildProxy(),
    });
    closeSettings();
    if (res && res.proxy_changed) showAlert("代理已更新", "已切换代理并重连所有账号，下次操作生效。");
  } catch (e) {
    setStatus($("settings-status"), "保存失败：" + e.message, "err");
  } finally {
    btn.disabled = false;
  }
}

$("btn-settings").onclick = openSettings;
$("settings-close").onclick = closeSettings;
$("settings-cancel").onclick = closeSettings;
$("settings-save").onclick = saveSettings;

// test the currently-entered proxy (no need to save first)
async function testProxy() {
  const btn = $("proxy-test");
  const host = $("set-proxy-host").value.trim();
  btn.disabled = true;
  setStatus($("settings-status"), host ? "正在通过代理连接 Telegram…" : "正在测试直连 Telegram…");
  try {
    const res = await api("POST", "/settings/proxy-test", { proxy: buildProxy() });
    if (res && res.ok) setStatus($("settings-status"), "✓ 可用：成功连接到 Telegram", "ok");
    else setStatus($("settings-status"), "✗ 不可用：" + ((res && res.error) || "连接失败"), "err");
  } catch (e) {
    setStatus($("settings-status"), "✗ 测试失败：" + e.message, "err");
  } finally {
    btn.disabled = false;
  }
}
$("proxy-test").onclick = testProxy;

// --- backup / recover (download / upload the .tdl dump) ---

// backup: a GET with Content-Disposition; an anchor click lets the browser save
// it straight to disk (no need to buffer the bytes in JS).
function downloadBackup() {
  const a = document.createElement("a");
  a.href = "/api/backup";
  a.download = "";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

async function recoverBackup(file) {
  const ok = await showConfirm(
    "恢复数据",
    "将用该备份合并导入账号数据，覆盖同名账号的 session/缓存（不会删除备份里没有的账号）。确定继续？",
    { okText: "恢复", danger: true }
  );
  if (!ok) return;
  setStatus($("settings-status"), "恢复中…");
  try {
    const fd = new FormData();
    fd.append("file", file);
    const res = await fetch("/api/recover", { method: "POST", body: fd });
    const data = await res.json().catch(() => null);
    if (!res.ok) throw new Error((data && data.error) || ("HTTP " + res.status));
    setStatus($("settings-status"), "✓ 恢复成功", "ok");
    await loadAccounts();
    showAlert("恢复成功", "数据已合并导入，账号已重连。如某账号未生效，可在账号栏点 ⟳ 刷新或重启 server。");
  } catch (e) {
    setStatus($("settings-status"), "✗ 恢复失败：" + e.message, "err");
  }
}

$("btn-backup").onclick = downloadBackup;
$("btn-recover").onclick = () => $("recover-file").click();
$("recover-file").onchange = (e) => {
  const file = e.target.files && e.target.files[0];
  e.target.value = ""; // allow re-selecting the same file later
  if (file) recoverBackup(file);
};

$("settings-modal").addEventListener("click", (e) => { if (e.target === $("settings-modal")) closeSettings(); });
$("settings-modal").addEventListener("keydown", (e) => {
  if (e.key === "Escape") { e.preventDefault(); closeSettings(); }
  else if (e.key === "Enter" && e.target.tagName !== "BUTTON") { e.preventDefault(); saveSettings(); }
});

// back-to-top button: show after the conversation list scrolls down a bit
const chatItemsEl = $("chat-items"), chatToTop = $("chat-totop");
chatItemsEl.addEventListener("scroll", () => {
  chatToTop.classList.toggle("show", chatItemsEl.scrollTop > 300);
});
chatToTop.onclick = () => chatItemsEl.scrollTo({ top: 0, behavior: "smooth" });

// --- search (filters the cached conversation list) ---
$("chat-search").addEventListener("input", (e) => { chatSearch = e.target.value.trim(); renderChatList(); });
$("chat-folders").addEventListener("wheel", (e) => {
  const el = $("chat-folders");
  if (el.classList.contains("hidden")) return;
  if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
  e.preventDefault();
  el.scrollLeft += e.deltaY;
}, { passive: false });

function chatMatchesSearch(c, q) {
  if (!q) return true;
  const s = q.toLowerCase();
  return String(c.visible_name || "").toLowerCase().includes(s)
    || String(c.username || "").toLowerCase().includes(s)
    || String(c.id || "").includes(s);
}

// Synthesize conversation rows for private chats where another local account
// sent a file TO the current account but that sender isn't in our cached dialog
// list yet. Without this, "the conversation with A" wouldn't appear under B
// until B refreshes its chat list. Rows use the sender's user id, so when the
// real dialog later appears (same id) they merge with no duplicate / color flip.
function receivedChats() {
  const myId = selfIdOf(currentNs);
  if (!myId) return [];
  const have = new Set(chats.map((c) => String(c.id)));
  const out = [], seen = new Set();
  allTasks.forEach((t) => {
    if (t.namespace === currentNs) return;
    if (String(t.chat_id) !== String(myId)) return; // not a private send to me
    const sid = selfIdOf(t.namespace);
    if (!sid) return;
    const k = String(sid);
    if (have.has(k) || seen.has(k)) return;
    seen.add(k);
    const a = findAccount(t.namespace);
    out.push({
      kind: "normal", synthetic: true, id: sid, type: "private",
      visible_name: accNick(t.namespace),
      username: (a && a.self && a.self.username) || "",
    });
  });
  return out;
}

let listSig = null;
let folderSig = null;

function renderFolderBar() {
  const bar = $("chat-folders");
  if (!currentNs || !chatFolders.length) {
    if (activeFolder !== "all") activeFolder = "all";
    bar.classList.add("hidden");
    bar.innerHTML = "";
    folderSig = "";
    return;
  }

  const sig = currentNs + "|" + activeFolder + "|" + chatFolders.map((f) => {
    return f.id + ":" + folderLabel(f) + ":" + folderChatCount(f.id);
  }).join(",");
  if (sig === folderSig) return;
  folderSig = sig;

  bar.classList.remove("hidden");
  bar.innerHTML = "";

  const make = (key, label, count) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "folder-chip" + (activeFolder === key ? " active" : "");
    b.title = label;
    b.innerHTML = `<span class="folder-chip-label">${escapeHtml(label)}</span><span class="folder-chip-count">${count}</span>`;
    b.onclick = () => {
      activeFolder = key;
      listSig = null;
      renderChatList();
    };
    return b;
  };

  bar.appendChild(make("all", "全部", chats.length + 1));
  chatFolders.forEach((f) => bar.appendChild(make(folderKey(f), folderLabel(f), folderChatCount(f.id))));
}

function renderChatList() {
  const list = $("chat-items");
  renderFolderBar();

  // build the ordered set first, or an empty-state message
  let ordered = null, emptyMsg = null;
  if (!currentNs) {
    emptyMsg = "请先登录账号";
  } else {
    const q = chatSearch;
    const all = chats.concat(receivedChats());
    const items = all.filter((c) => chatMatchesFolder(c) && chatMatchesSearch(c, q));
    // sort: Telegram folder pins first, then by last-sent time desc, then by name
    items.sort((a, b) => {
      const fa = activeFolderPinRank(a), fb = activeFolderPinRank(b);
      if (fa !== fb) {
        if (fa >= 0 && fb >= 0) return fa - fb;
        if (fa >= 0) return -1;
        if (fb >= 0) return 1;
      }
      const ta = lastTaskForChat(a), tb = lastTaskForChat(b);
      const da = ta ? new Date(ta.created_at).getTime() : 0;
      const db = tb ? new Date(tb.created_at).getTime() : 0;
      if (da !== db) return db - da;
      return String(a.visible_name || "").localeCompare(String(b.visible_name || ""));
    });

    ordered = [];
    if (activeFolder === "all" && (!q || "saved messages 收藏夹".includes(q.toLowerCase()))) ordered.push(savedEntry()); // pinned to top
    ordered.push(...items);
    if (!ordered.length) {
      if (activeFolder !== "all") emptyMsg = q ? "该分组没有匹配的会话" : "该分组没有会话";
      else emptyMsg = all.length ? "没有匹配的会话" : "还没有会话缓存<br>点下方“刷新会话列表”拉取";
      ordered = null;
    }
  }

  // skip the DOM rebuild when nothing visible changed, so a 1s poll doesn't keep
  // wiping the list you're inspecting. The signature excludes live upload % (which
  // doesn't affect the list), so active uploads don't churn it either.
  const selKey = selectedChat ? chatKey(selectedChat) : "";
  let sig;
  if (emptyMsg !== null) {
    sig = "empty:" + emptyMsg;
  } else {
    sig = currentNs + "|" + chatSearch + "|" + activeFolder + "|" + selKey + "|" + ordered.map((c) => {
      const last = lastTaskForChat(c);
      const pinned = c.kind === "normal" && isChatPinnedInView(c) ? 1 : 0;
      return chatKey(c) + ":" + (c.visible_name || "") + ":" + pinned + ":" + activeFolderPinRank(c) + ":" + ((c.folder_ids || []).join(".")) + ":" + (last ? last.id + last.namespace : "");
    }).join(",");
  }
  if (sig === listSig) return;
  listSig = sig;

  if (emptyMsg !== null) { list.innerHTML = `<li class='chat-empty'>${emptyMsg}</li>`; return; }
  list.innerHTML = "";
  ordered.forEach((c) => list.appendChild(chatRow(c)));
}

function chatRow(c) {
  const li = document.createElement("li");
  li.className = "chat-row" + (selectedChat && chatKey(selectedChat) === chatKey(c) ? " active" : "");

  // full-text tooltip — row names are truncated with ellipsis
  const dispName = c.visible_name || c.username || String(c.id);
  const tip = [];
  if (c.username) tip.push("@" + c.username);
  tip.push(typeLabel(c));
  if (c.kind === "normal" && c.id) tip.push("id " + c.id);
  li.title = dispName + (tip.length ? "\n" + tip.join(" · ") : "");

  const av = document.createElement("div");
  av.className = "avatar";
  setAvatar(av, c.visible_name || c.username || c.id, chatColorKey(c));

  const main = document.createElement("div");
  main.className = "chat-row-main";

  const top = document.createElement("div");
  top.className = "chat-row-top";
  const name = document.createElement("span");
  name.className = "chat-row-name";
  const pinned = c.kind === "normal" && isChatPinnedInView(c);
  name.innerHTML = (pinned ? "<span class='pin'>📌</span>" : "") + escapeHtml(c.visible_name || c.username || String(c.id));
  const time = document.createElement("span");
  time.className = "chat-row-time";

  const last = lastTaskForChat(c);
  if (last) time.textContent = fmtListTime(new Date(last.created_at));
  top.append(name, time);

  const sub = document.createElement("div");
  sub.className = "chat-row-sub";
  if (last) {
    const nick = last.namespace !== currentNs ? `<span class='sub-nick'>${escapeHtml(accNick(last.namespace))}: </span>` : "";
    sub.innerHTML = nick + escapeHtml(taskFileName(last));
  }

  main.append(top, sub);
  li.append(av, main);
  li.onclick = () => selectChat(c);
  li.oncontextmenu = (e) => { e.preventDefault(); openChatCtx(e, c); };
  return li;
}

// --- select & render the conversation window ---
function selectChat(c) {
  selectedChat = c;
  renderChatList(); // refresh active highlight
  renderWindow();
  setTimeout(() => $("cw-path").focus(), 0);
}

function renderWindow() {
  const empty = $("cw-empty"), mainEl = $("cw-main");
  if (!selectedChat) {
    empty.classList.remove("hidden");
    mainEl.classList.add("hidden");
    return;
  }
  empty.classList.add("hidden");
  mainEl.classList.remove("hidden");

  const c = selectedChat;
  setAvatar($("cw-avatar"), c.visible_name || c.username || c.id, chatColorKey(c));
  $("cw-title").textContent = c.visible_name || c.username || String(c.id);
  const meta = [];
  if (c.username) meta.push("@" + c.username);
  meta.push(typeLabel(c));
  if (c.kind === "normal" && c.id) meta.push("id " + c.id);
  $("cw-meta").textContent = meta.join(" · ");
  $("cw-title").title = $("cw-title").textContent + (meta.length ? "\n" + meta.join(" · ") : ""); // tooltip: title truncates too

  // no-permission gating: when the current account can't send media here (from
  // the cached dialog rights), hide the composer and show a notice instead.
  const forbidden = c.kind === "normal" && !!c.send_forbidden;
  $("cw-forbidden").textContent = forbidden
    ? "🚫 无权限发送" + (c.send_forbid_reason ? "：" + c.send_forbid_reason : "")
    : "";
  $("cw-forbidden").classList.toggle("hidden", !forbidden);
  $("cw-composer").classList.toggle("hidden", forbidden);

  renderHistory();
}

const STATUS_GLYPH = { done: "✓", running: "●", queued: "◷", error: "⚠" };

let histSig = null;

function renderHistory() {
  const hist = $("cw-history");
  const c = selectedChat;
  const ts = tasksForChat(c).slice().sort((a, b) => new Date(a.created_at) - new Date(b.created_at));

  if (!ts.length) {
    if (histSig !== "empty") {
      hist.innerHTML = "<div class='hist-empty'>还没有发送记录，在下面发第一个文件吧</div>";
      histSig = "empty";
    }
    return;
  }

  // structural signature (everything that affects the DOM except live upload %).
  // When only progress changed we update the rings in place and return, so a 1s
  // poll doesn't blow away the nodes you're inspecting in F12.
  // currentNs is part of the key because left/right (out = t.namespace===currentNs)
  // depends on it: a shared group/channel has the same chatKey for every account,
  // so without currentNs, switching accounts on such a conversation would keep the
  // stale alignment (an in-flight bubble stuck on the wrong side until it finishes).
  const sig = currentNs + "|" + chatKey(c) + "|" + ts.map((t) => `${t.id}:${t.status}:${t.namespace}:${taskFileName(t)}:${t.caption || ""}`).join(",");
  if (sig === histSig) {
    ts.forEach((t) => { if (t.status === "running" || t.status === "queued") updateRing(t); });
    return;
  }
  histSig = sig;

  // keep the view pinned to the bottom if it already was
  const atBottom = hist.scrollHeight - hist.scrollTop - hist.clientHeight < 40;

  hist.innerHTML = "";
  let lastDay = "", prevSender = null;
  ts.forEach((t, i) => {
    const d = new Date(t.created_at);
    const day = fmtDateDivider(d);
    if (day !== lastDay) {
      const div = document.createElement("div");
      div.className = "cw-date";
      div.textContent = day;
      hist.appendChild(div);
      lastDay = day;
      prevSender = null; // a date divider breaks the sender run
    }
    const out = t.namespace === currentNs;
    // IM convention: show the sender's name only above the first of a run of
    // messages from that (other) account; own messages are right-aligned, no name
    const showSender = !out && t.namespace !== prevSender;
    // Telegram convention: the avatar rides on the LAST message of a run, so it's
    // shown when the next message starts a new run — a different sender, a new day
    // (date divider breaks the run), or the end of the history.
    const next = ts[i + 1];
    const endsRun = !next
      || next.namespace !== t.namespace
      || fmtDateDivider(new Date(next.created_at)) !== day;
    const showAvatar = !out && endsRun;
    hist.appendChild(msgBubble(t, d, showSender, showAvatar));
    prevSender = t.namespace;
  });

  if (atBottom) hist.scrollTop = hist.scrollHeight;
}

// minimal markdown -> HTML for caption display (mirrors the backend subset in
// app/web/markdown.go): `code`, [text](url), **bold**, *italic*/_italic_, ~~strike~~
function renderCaptionMd(s) {
  const esc = (t) => String(t).replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));
  const escAttr = (t) => esc(t).replace(/"/g, "&quot;");
  const r = [...String(s)], n = r.length;
  const readUntil = (start, close) => {
    const cl = [...close];
    for (let j = start; j + cl.length <= n; j++) {
      if (r.slice(j, j + cl.length).join("") === close) return [r.slice(start, j).join(""), j + cl.length];
    }
    return null;
  };
  let out = "", i = 0, m;
  while (i < n) {
    const c = r[i];
    if (c === "`" && (m = readUntil(i + 1, "`"))) { out += "<code>" + esc(m[0]) + "</code>"; i = m[1]; continue; }
    if (c === "[") {
      const t = readUntil(i + 1, "]");
      if (t && r[t[1]] === "(") {
        const u = readUntil(t[1] + 1, ")");
        if (u) { out += `<a href="${escAttr(u[0].trim())}" target="_blank" rel="noopener">${esc(t[0])}</a>`; i = u[1]; continue; }
      }
    }
    if (c === "*" && r[i + 1] === "*" && (m = readUntil(i + 2, "**")) && m[0]) { out += "<b>" + esc(m[0]) + "</b>"; i = m[1]; continue; }
    if (c === "~" && r[i + 1] === "~" && (m = readUntil(i + 2, "~~")) && m[0]) { out += "<s>" + esc(m[0]) + "</s>"; i = m[1]; continue; }
    if ((c === "*" || c === "_") && (m = readUntil(i + 1, c)) && m[0]) { out += "<i>" + esc(m[0]) + "</i>"; i = m[1]; continue; }
    out += esc(c); i++;
  }
  return out;
}

function msgBubble(t, d, showSender, showAvatar) {
  const out = t.namespace === currentNs; // own account -> right; other accounts -> left
  const wrap = document.createElement("div");
  wrap.className = "msg " + (out ? "out" : "in");

  if (!out) {
    // Telegram convention: the avatar rides on the LAST message of a run
    // (showAvatar, bottom-aligned via .msg align-items); the other messages in the
    // run get a transparent spacer so bubbles stay aligned. The sender's name
    // still sits on the first message of the run (showSender).
    const av = document.createElement("div");
    av.className = "avatar sm" + (showAvatar ? "" : " spacer");
    if (showAvatar) setAvatar(av, accNick(t.namespace), "acc:" + t.namespace);
    wrap.appendChild(av);
  }

  const bubble = document.createElement("div");
  bubble.className = "bubble";
  const glyph = STATUS_GLYPH[t.status] || "";
  const errTitle = t.status === "error" && t.error ? ` title='${escapeHtml(t.error)}'` : "";

  // album: one bubble for the whole group (file list + total size); otherwise a
  // single file
  let head, size;
  if (t.album) {
    const fs = t.files || [];
    head = `<div class='fname'>📚 相册 · ${fs.length} 个文件</div>` +
      `<div class='album-list'>${fs.map((f) => escapeHtml(f.name)).join("<br>")}</div>`;
    size = fmtSize(fs.reduce((a, f) => a + (f.size > 0 ? f.size : 0), 0));
  } else {
    head = `<div class='fname'>${escapeHtml(taskFileName(t))}</div>`;
    size = t.files && t.files.length ? fmtSize(t.files[0].size) : "";
  }

  const content = document.createElement("div");
  content.className = "bubble-content";
  content.innerHTML =
    (showSender ? `<div class='sender'>${escapeHtml(accNick(t.namespace))}</div>` : "") +
    head +
    (t.caption ? `<div class='caption'>${renderCaptionMd(t.caption)}</div>` : "") +
    (t.status === "error" && t.error ? `<div class='berr'>${escapeHtml(t.error)}</div>` : "") +
    `<div class='meta'>` +
      `<span class='msize'>${escapeHtml(size)}</span>` +
      `<span class='mright'><span class='mtime'>${hm(d)}</span><span class='mstatus ${t.status}'${errTitle}>${glyph}</span></span>` +
    `</div>`;

  // in-flight: the spinning progress ring (arc grows with %) + cancel X lives
  // INSIDE the bubble, on the outer side (left for own/out, right for others/in)
  if (t.status === "running" || t.status === "queued") {
    bubble.classList.add("with-prog");
    const ring = progRing(t);
    if (out) bubble.append(ring, content); else bubble.append(content, ring);
  } else {
    bubble.appendChild(content);
  }

  wrap.appendChild(bubble);
  return wrap;
}

const PROG_C = 2 * Math.PI * 16; // circumference for r=16 in the 36x36 viewBox

function progRing(t) {
  const wrap = document.createElement("div");
  wrap.className = "prog";
  wrap.dataset.id = t.id;
  const dash = (Math.max(0, Math.min(100, t.progress || 0)) / 100) * PROG_C;
  wrap.innerHTML =
    `<svg viewBox="0 0 36 36" class="prog-svg">` +
      `<circle class="prog-track" cx="18" cy="18" r="16"></circle>` +
      `<circle class="prog-arc" cx="18" cy="18" r="16" stroke-dasharray="${dash.toFixed(2)} ${PROG_C.toFixed(2)}" stroke-linecap="round"></circle>` +
    `</svg>` +
    `<button class="prog-x" title="取消上传">✕</button>`;
  wrap.querySelector(".prog-x").onclick = (e) => { e.stopPropagation(); cancelTask(t.id); };
  return wrap;
}

// update an existing ring's arc length in place (no DOM rebuild)
function updateRing(t) {
  const arc = $("cw-history").querySelector(`.prog[data-id="${t.id}"] .prog-arc`);
  if (!arc) return;
  const dash = (Math.max(0, Math.min(100, t.progress || 0)) / 100) * PROG_C;
  arc.setAttribute("stroke-dasharray", `${dash.toFixed(2)} ${PROG_C.toFixed(2)}`);
}

async function cancelTask(id) {
  try { await api("DELETE", `/tasks/${encodeURIComponent(id)}`); } catch (_) { /* ignore */ }
  await refreshTasks(); // the task is gone -> its bubble disappears
}

// --- composer: send an upload to the selected conversation ---
$("cw-send").onclick = async () => {
  const ns = currentNs;
  const status = $("cw-status");
  if (!ns) { setStatus(status, "请先登录并选择账号", "err"); return; }
  if (!selectedChat) { setStatus(status, "请先选择一个会话", "err"); return; }
  if (selectedChat.kind === "normal" && selectedChat.send_forbidden) {
    setStatus(status, "无权限发送" + (selectedChat.send_forbid_reason ? "：" + selectedChat.send_forbid_reason : ""), "err");
    return;
  }

  const path = $("cw-path").value.trim();
  if (!path) { setStatus(status, "请填写文件/目录路径", "err"); return; }

  const c = selectedChat;
  // 'to': saved -> empty (= Saved Messages); normal -> chat id
  let to = "";
  if (c.kind === "normal") to = String(c.id);

  const body = {
    path,
    to,
    chat_id: c.kind === "normal" ? c.id : (Number(c.id) || 0),
    chat_name: c.visible_name || c.username || String(c.id),
    topic: parseInt($("cw-topic").value, 10) || 0,
    caption: $("cw-caption").value,
    photo: $("cw-photo").checked,
    includes: splitExts($("cw-includes").value),
    excludes: splitExts($("cw-excludes").value),
  };

  $("cw-send").disabled = true;
  setStatus(status, "正在提交…", "info");
  try {
    await api("POST", `/accounts/${encodeURIComponent(ns)}/upload`, body);
    $("cw-path").value = "";
    setStatus(status, "", "");
    await refreshTasks(); // show the new bubble immediately
    $("cw-history").scrollTop = $("cw-history").scrollHeight;
  } catch (e) {
    setStatus(status, e.message, "err");
  } finally {
    $("cw-send").disabled = false;
  }
};
$("cw-path").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); $("cw-send").click(); } });

// --- drag-drop / file-picker upload (browser bytes -> server staging) ---
function fileKind(f) {
  const t = (f.type || "").toLowerCase();
  if (t.startsWith("image/")) return "image";
  if (t.startsWith("video/")) return "video";
  if (t.startsWith("audio/")) return "audio";
  const ext = (f.name.split(".").pop() || "").toLowerCase();
  if (["jpg", "jpeg", "png", "gif", "webp", "bmp"].includes(ext)) return "image";
  if (["mp4", "mkv", "mov", "avi", "webm", "m4v"].includes(ext)) return "video";
  if (["mp3", "flac", "aac", "ogg", "wav", "m4a"].includes(ext)) return "audio";
  return "other";
}

let pendingFiles = []; // [{ file, kind, mode: 'media' | 'file' }]

$("cw-drop").onclick = () => $("cw-file").click();
$("cw-drop").addEventListener("keydown", (e) => {
  if ($("send-modal").classList.contains("open")) return; // dialog open: Enter shouldn't re-open the picker
  if (e.key === "Enter" || e.key === " ") { e.preventDefault(); $("cw-file").click(); }
});
$("cw-file").addEventListener("change", (e) => { collectFiles(e.target.files); e.target.value = ""; });

// drag anywhere over the open conversation window
const cwMain = $("cw-main");
["dragenter", "dragover"].forEach((ev) => cwMain.addEventListener(ev, (e) => {
  if (!e.dataTransfer || ![...e.dataTransfer.types].includes("Files")) return;
  e.preventDefault();
  cwMain.classList.add("dragging");
}));
cwMain.addEventListener("dragleave", (e) => {
  if (e.relatedTarget && cwMain.contains(e.relatedTarget)) return; // still inside
  cwMain.classList.remove("dragging");
});
cwMain.addEventListener("drop", (e) => {
  cwMain.classList.remove("dragging");
  if (!e.dataTransfer || !e.dataTransfer.files.length) return;
  e.preventDefault();
  collectFiles(e.dataTransfer.files);
});

function collectFiles(fileList) {
  if (!selectedChat) { showAlert("先选会话", "请先在左侧选择一个会话，再发送文件。"); return; }
  if (selectedChat.kind === "normal" && selectedChat.send_forbidden) {
    showAlert("无权限发送", selectedChat.send_forbid_reason || "你在该会话没有发送权限。");
    return;
  }
  const files = [...fileList];
  if (!files.length) return;
  pendingFiles = files.map((f) => {
    const kind = fileKind(f);
    return { file: f, kind, mode: kind === "other" ? "file" : "media" };
  });
  openSendDialog();
}

// addMoreFiles appends to the open dialog's list (the "＋ 添加文件" button) instead
// of replacing it like collectFiles. Skips files already in the list (same
// name+size+lastModified) so re-picking the same file doesn't duplicate it.
function addMoreFiles(fileList) {
  const incoming = [...fileList];
  if (!incoming.length) return;
  const seen = new Set(pendingFiles.map((it) => it.file.name + "|" + it.file.size + "|" + it.file.lastModified));
  let added = 0;
  incoming.forEach((f) => {
    const key = f.name + "|" + f.size + "|" + f.lastModified;
    if (seen.has(key)) return;
    seen.add(key);
    const kind = fileKind(f);
    pendingFiles.push({ file: f, kind, mode: kind === "other" ? "file" : "media" });
    added++;
  });
  if (!added) return;
  renderSendFiles();
  refreshSendOptionsUI(); // re-evaluate album conflict + preview; keeps current toggle choices
}

const MEDIA_LABEL = { image: "照片", video: "视频", audio: "音频" };

// albumCategory groups a pending file by what kind of Telegram media group it can
// join: photos/videos mix freely ("visual"), audio forms its own group, everything
// else is a plain document. Telegram rejects a group spanning >1 category.
function albumCategory(it) {
  if (it.kind === "image" || it.kind === "video") return "visual";
  if (it.kind === "audio") return "audio";
  return "doc";
}

// hasAlbumConflict is true when the selection can't be one valid media group
// because it mixes categories (e.g. an image + a non-media file).
function hasAlbumConflict(files) {
  if (files.length < 2) return false;
  const first = albumCategory(files[0]);
  return files.some((it) => albumCategory(it) !== first);
}

let albumConflict = false;

// refreshSendOptionsUI swaps the album toggle for the conflict-only "send all as
// files" toggle, and hides the per-file selectors whenever we're grouping.
function refreshSendOptionsUI() {
  albumConflict = hasAlbumConflict(pendingFiles);
  $("send-album-row").style.display = albumConflict ? "none" : "";
  $("send-asfile-row").style.display = albumConflict ? "" : "none";
  const grouping = albumConflict ? $("send-asfile").checked : $("send-album").checked;
  $("send-files").classList.toggle("album-mode", grouping);
  renderSendPreview();
}

// --- send preview: a mock of how the resulting message(s) will look ---
let previewUrls = []; // object URLs created for image thumbnails, revoked on re-render/close

function clearPreviewUrls() {
  previewUrls.forEach((u) => URL.revokeObjectURL(u));
  previewUrls = [];
}
function thumbURL(file) {
  const u = URL.createObjectURL(file);
  previewUrls.push(u);
  return u;
}

// build one preview bubble laid out like the user's own (right-aligned) message
function pvBubble(innerHTML) {
  const wrap = document.createElement("div");
  wrap.className = "msg out";
  const bubble = document.createElement("div");
  bubble.className = "bubble";
  const content = document.createElement("div");
  content.className = "bubble-content";
  content.innerHTML = innerHTML;
  bubble.appendChild(content);
  wrap.appendChild(bubble);
  return wrap;
}

function captionHTML(caption) {
  return caption.trim() ? `<div class='caption'>${renderCaptionMd(caption)}</div>` : "";
}

// a single file → one bubble; images sent as photos get a thumbnail
function previewFileBubble(it, caption) {
  const asPhoto = it.mode === "media" && it.kind === "image";
  let html = "";
  if (asPhoto) html += `<img class="pv-thumb" src="${thumbURL(it.file)}" alt="" />`;
  html += `<div class='fname'>${escapeHtml(it.file.name)}</div>`;
  html += captionHTML(caption);
  html += `<div class='meta'><span class='msize'>${escapeHtml(fmtSize(it.file.size))}</span></div>`;
  return pvBubble(html);
}

// the whole group → one album bubble (image thumbnails unless forced to files)
function previewAlbumBubble(files, caption, asFileAlbum, groups) {
  let html = `<div class='fname'>📚 相册 · ${files.length} 个文件` +
    (groups > 1 ? `（自动分成 ${groups} 组发送）` : "") + `</div>`;
  if (!asFileAlbum) {
    const imgs = files.filter((it) => it.kind === "image");
    if (imgs.length) {
      html += `<div class='pv-grid'>` +
        imgs.map((it) => `<img class="pv-thumb sm" src="${thumbURL(it.file)}" alt="" />`).join("") +
        `</div>`;
    }
  }
  html += `<div class='album-list'>${files.map((f) => escapeHtml(f.file.name)).join("<br>")}</div>`;
  html += captionHTML(caption);
  const total = files.reduce((a, f) => a + (f.file.size > 0 ? f.file.size : 0), 0);
  html += `<div class='meta'><span class='msize'>${escapeHtml(fmtSize(total))}</span></div>`;
  return pvBubble(html);
}

function renderSendPreview() {
  const box = $("send-preview");
  clearPreviewUrls();
  box.innerHTML = "";
  if (!pendingFiles.length) return;

  const caption = $("send-caption").value;
  // mirror the grouping decision in send-go
  const asFileAlbum = albumConflict && $("send-asfile").checked;
  const grouping = albumConflict ? asFileAlbum : $("send-album").checked;

  if (grouping) {
    // album mode: caption only lands on the first group's first item
    const groups = Math.ceil(pendingFiles.length / 10);
    box.appendChild(previewAlbumBubble(pendingFiles, caption, asFileAlbum, groups));
  } else {
    // per-file: the shared caption is applied to every file
    pendingFiles.forEach((it) => box.appendChild(previewFileBubble(it, caption)));
  }
}


function openSendDialog() {
  const c = selectedChat;
  $("send-target").innerHTML = "目标：<b>" + escapeHtml(c.visible_name || c.username || String(c.id)) + "</b>";
  $("send-caption").value = $("cw-caption").value;
  renderSendFiles();
  $("send-album").checked = pendingFiles.length >= 2; // multiple files default to an album
  $("send-asfile").checked = true; // conflict default: merge everything into one document album
  refreshSendOptionsUI();
  setStatus($("send-status"), "");
  $("send-modal").classList.add("open");
  // move focus off the drop-bar (which has an Enter -> open-picker handler) and
  // into the modal, so Enter sends instead of re-triggering the file picker
  if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
  setTimeout(() => $("send-caption").focus(), 30);
}

function renderSendFiles() {
  const box = $("send-files");
  box.innerHTML = "";
  pendingFiles.forEach((it, idx) => {
    const row = document.createElement("div");
    row.className = "send-file";

    const main = document.createElement("div");
    main.className = "sf-main";
    main.innerHTML = `<div class='sf-name'>${escapeHtml(it.file.name)}</div><div class='sf-size'>${escapeHtml(fmtSize(it.file.size))}</div>`;
    row.appendChild(main);

    if (it.kind !== "other") {
      const sel = document.createElement("select");
      sel.innerHTML = `<option value='media'>作为${MEDIA_LABEL[it.kind]}</option><option value='file'>作为文件</option>`;
      sel.value = it.mode;
      sel.onchange = () => { pendingFiles[idx].mode = sel.value; renderSendPreview(); };
      row.appendChild(sel);
    } else {
      const tag = document.createElement("span");
      tag.className = "sf-size";
      tag.textContent = "文件";
      row.appendChild(tag);
    }

    const rm = iconBtn("✕", "移除", () => {
      pendingFiles.splice(idx, 1);
      if (!pendingFiles.length) closeSendDialog(); else { renderSendFiles(); refreshSendOptionsUI(); }
    });
    row.appendChild(rm);
    box.appendChild(row);
  });
}

function closeSendDialog() { $("send-modal").classList.remove("open"); pendingFiles = []; clearPreviewUrls(); $("send-preview").innerHTML = ""; }
$("send-close").onclick = closeSendDialog;
$("send-cancel").onclick = closeSendDialog;
$("send-modal").addEventListener("click", (e) => { if (e.target === $("send-modal")) closeSendDialog(); });
// Enter (off the buttons) sends; Esc closes
$("send-modal").addEventListener("keydown", (e) => {
  if (e.key === "Escape") { e.preventDefault(); closeSendDialog(); return; }
  if (e.key === "Enter" && e.target.tagName !== "BUTTON") { e.preventDefault(); $("send-go").click(); }
});
$("send-album").addEventListener("change", refreshSendOptionsUI);
$("send-asfile").addEventListener("change", refreshSendOptionsUI);
$("send-caption").addEventListener("input", renderSendPreview);

// "＋ 添加文件": pick more files and append them to the current list
$("send-add").onclick = () => $("send-add-file").click();
$("send-add-file").addEventListener("change", (e) => { addMoreFiles(e.target.files); e.target.value = ""; });

$("send-go").onclick = async () => {
  const ns = currentNs, c = selectedChat;
  const status = $("send-status");
  if (!ns || !c) { setStatus(status, "请先选择账号和会话", "err"); return; }
  if (!pendingFiles.length) { closeSendDialog(); return; }

  // resolve the destination once (same rules as the composer)
  let to = "";
  if (c.kind === "normal") to = String(c.id);
  const meta = {
    to,
    chatId: c.kind === "normal" ? c.id : (Number(c.id) || 0),
    chatName: c.visible_name || c.username || String(c.id),
    topic: parseInt($("cw-topic").value, 10) || 0,
    caption: $("send-caption").value,
  };

  // Grouping decision:
  // - no conflict: honor the "作为相册" toggle (mixed media → one media group).
  // - conflict + "全部作为文件" checked: force every item to a document so it's
  //   one valid all-document album.
  // - conflict + unchecked: fall through to the per-file loop (分开逐个发送).
  const asFileAlbum = albumConflict && $("send-asfile").checked;
  const sendAsAlbum = albumConflict ? asFileAlbum : $("send-album").checked;

  // album mode: send as media group(s) of <=10, in order (one task per group)
  if (sendAsAlbum) {
    const all = pendingFiles.map((it) => it.file); // grab the bytes refs before closing
    const groups = [];
    for (let i = 0; i < all.length; i += 10) groups.push(all.slice(i, i + 10));
    // The real Telegram upload runs server-side in the background; the POST we
    // await only streams the bytes to the local server. Close the dialog now —
    // the File refs stay alive inside each fetch's FormData, so the transfers
    // keep going, and the bubbles + progress rings render from the task poll.
    closeSendDialog();
    (async () => {
      for (let g = 0; g < groups.length; g++) {
        try {
          await uploadAlbumGroup(ns, meta, groups[g], asFileAlbum);
          await refreshTasks();
          $("cw-history").scrollTop = $("cw-history").scrollHeight;
        } catch (e) {
          showAlert("相册发送失败", e.message);
          return;
        }
      }
    })();
    return;
  }

  const files = pendingFiles.slice();
  // same as above: close immediately, stream the files in the background. The
  // loop stays sequential so the tasks (and their bubbles) keep top-to-bottom order.
  closeSendDialog();
  (async () => {
    for (const it of files) {
      try {
        await uploadOneFile(ns, meta, it);
        await refreshTasks();
        $("cw-history").scrollTop = $("cw-history").scrollHeight;
      } catch (e) {
        showAlert("发送失败", `「${it.file.name}」：${e.message}`);
        return;
      }
    }
  })();
};

function uploadOneFile(ns, meta, it) {
  const fd = new FormData();
  fd.append("to", meta.to);
  fd.append("chat_id", String(meta.chatId));
  fd.append("chat_name", meta.chatName);
  fd.append("topic", String(meta.topic));
  fd.append("caption", meta.caption);
  let photo = false, asFile = false;
  if (it.mode === "file") asFile = true;
  else if (it.kind === "image") photo = true;
  fd.append("photo", photo ? "true" : "false");
  fd.append("as_file", asFile ? "true" : "false");
  fd.append("file", it.file, it.file.name); // the actual bytes
  return fetch(`/api/accounts/${encodeURIComponent(ns)}/upload-files`, { method: "POST", body: fd })
    .then(async (res) => {
      if (!res.ok) {
        let msg = "HTTP " + res.status;
        try { const j = await res.json(); if (j && j.error) msg = j.error; } catch (_) { /* ignore */ }
        throw new Error(msg);
      }
    });
}

// album: post a group of files (<=10) as one Telegram media group. asFile forces
// every item to a plain document (for a type-mixed selection that can't otherwise
// share a group).
function uploadAlbumGroup(ns, meta, fileArr, asFile) {
  const fd = new FormData();
  fd.append("to", meta.to);
  fd.append("chat_id", String(meta.chatId));
  fd.append("chat_name", meta.chatName);
  fd.append("topic", String(meta.topic));
  fd.append("caption", meta.caption);
  fd.append("as_file", asFile ? "true" : "false");
  fileArr.forEach((f) => fd.append("file", f, f.name)); // order preserved server-side
  return fetch(`/api/accounts/${encodeURIComponent(ns)}/upload-album`, { method: "POST", body: fd })
    .then(async (res) => {
      if (!res.ok) {
        let msg = "HTTP " + res.status;
        try { const j = await res.json(); if (j && j.error) msg = j.error; } catch (_) { /* ignore */ }
        throw new Error(msg);
      }
    });
}

// --- conversation context menu ---
const chatCtx = $("chat-ctx");
function openChatCtx(e, c) {
  chatCtx.innerHTML = "";
  const add = (label, fn, danger) => {
    const b = document.createElement("button");
    b.textContent = label;
    if (danger) b.className = "danger";
    b.onclick = () => { closeChatCtx(); fn(); };
    chatCtx.appendChild(b);
  };
  if (c.kind === "normal") {
    add("复制 ID", () => {
      if (navigator.clipboard) navigator.clipboard.writeText(String(c.id)).catch(() => showAlert("ID", String(c.id)));
      else showAlert("ID", String(c.id));
    });
  }
  add("刷新会话列表", refreshChats);

  chatCtx.classList.add("open");
  const w = chatCtx.offsetWidth, h = chatCtx.offsetHeight;
  chatCtx.style.left = Math.min(e.clientX, window.innerWidth - w - 8) + "px";
  chatCtx.style.top = Math.min(e.clientY, window.innerHeight - h - 8) + "px";
}
function closeChatCtx() { chatCtx.classList.remove("open"); }
document.addEventListener("click", closeChatCtx);
document.addEventListener("scroll", closeChatCtx, true);

// --- tasks (global overview tab) ---
function renderTasksTab() {
  const body = $("tasks-body");
  if (!allTasks.length) {
    body.innerHTML = "<tr><td colspan='6' class='muted'>暂无任务</td></tr>";
    return;
  }
  body.innerHTML = "";
  allTasks.forEach((t) => {
    const tr = document.createElement("tr");
    tr.innerHTML =
      `<td>#${t.id}</td>` +
      `<td>${escapeHtml(t.namespace)}</td>` +
      `<td>${escapeHtml(t.chat_name || t.target)}</td>` +
      `<td class='path' title='${escapeHtml((t.paths || []).join(", "))}'>${escapeHtml((t.paths || []).join(", "))}</td>` +
      `<td><span class='badge ${t.status}'>${t.status}</span></td>` +
      `<td class='err'>${escapeHtml(t.error || "")}</td>`;
    body.appendChild(tr);
  });
}

// poll the task list every 2s; refresh whichever view is active
async function refreshTasks() {
  try {
    allTasks = (await api("GET", "/tasks")) || [];
  } catch (_) {
    allTasks = allTasks || [];
  }
  if ($("tab-tasks").classList.contains("active")) renderTasksTab();
  if ($("tab-chats").classList.contains("active")) {
    renderChatList();
    if (selectedChat) renderHistory();
  }
}
setInterval(refreshTasks, 1000);

// --- init ---
loadAccounts();
refreshTasks();
