const STRINGS = {
  en: {
    inspect: "inspect", phone: "Phone", refresh: "Read the screen", screen: "Screen", tree: "Elements",
    element: "Element", recorder: "Recorder", pick_hint: "Click an element on the screen or in the list.",
    selectors: "Ways to find it", copy: "Copy", copied: "Copied", try: "Try it", ran: "Done",
    rec_about: "Use the phone as usual. Each tap and each text you type becomes a line of code. Back and home are not recorded; add d.back() yourself.",
    rec_start: "Start recording", rec_stop: "Stop recording", rec_clear: "Clear", rec_empty: "No steps yet.",
    reading: "Reading the screen", no_phone: "No phone is online. Pair one with droidline pair, then reload this page.",
    unique: "matches only this", shared: "{n} matches, this one is nth {i}",
    no_shot: "No screenshot ({err}). The outlines come from the element tree, so you can still pick elements.",
    theme_auto: "Auto", theme_light: "Light", theme_dark: "Dark", offline: "offline",
    a_text: "text", a_id: "id", a_desc: "desc", a_class: "class", a_package: "package", a_bounds: "bounds", a_flags: "flags",
  },
  ko: {
    inspect: "inspect", phone: "폰", refresh: "화면 읽기", screen: "화면", tree: "요소",
    element: "요소 정보", recorder: "녹화", pick_hint: "화면이나 목록에서 요소를 누르세요.",
    selectors: "찾는 방법", copy: "복사", copied: "복사했습니다", try: "실행해 보기", ran: "실행했습니다",
    rec_about: "평소처럼 폰을 쓰세요. 탭과 입력한 글자가 한 줄씩 코드가 됩니다. 뒤로 가기와 홈은 녹화되지 않으니 d.back()을 직접 넣으세요.",
    rec_start: "녹화 시작", rec_stop: "녹화 중지", rec_clear: "지우기", rec_empty: "아직 녹화된 단계가 없습니다.",
    reading: "화면을 읽는 중", no_phone: "온라인인 폰이 없습니다. droidline pair로 폰을 등록한 뒤 이 페이지를 새로 고치세요.",
    unique: "이 요소 하나만 맞음", shared: "{n}개가 맞음, 이 요소는 nth {i}",
    no_shot: "스크린샷 없음({err}). 윤곽선은 요소 트리로 그린 것이라 요소는 그대로 고를 수 있습니다.",
    theme_auto: "자동", theme_light: "밝게", theme_dark: "어둡게", offline: "오프라인",
    a_text: "text", a_id: "id", a_desc: "desc", a_class: "class", a_package: "package", a_bounds: "bounds", a_flags: "속성",
  },
  zh: {
    inspect: "inspect", phone: "手机", refresh: "读取界面", screen: "界面", tree: "元素",
    element: "元素信息", recorder: "录制", pick_hint: "点击界面上或列表中的元素。",
    selectors: "查找方式", copy: "复制", copied: "已复制", try: "试一下", ran: "已执行",
    rec_about: "像平时一样使用手机。每次点击和输入的文字都会变成一行代码。返回和主屏幕不会被录制，请自行加上 d.back()。",
    rec_start: "开始录制", rec_stop: "停止录制", rec_clear: "清空", rec_empty: "还没有录制的步骤。",
    reading: "正在读取界面", no_phone: "没有在线的手机。请先用 droidline pair 配对，再刷新此页面。",
    unique: "只匹配这个元素", shared: "匹配 {n} 个，这是 nth {i}",
    no_shot: "没有截图（{err}）。轮廓来自元素树，仍然可以选择元素。",
    theme_auto: "自动", theme_light: "浅色", theme_dark: "深色", offline: "离线",
    a_text: "text", a_id: "id", a_desc: "desc", a_class: "class", a_package: "package", a_bounds: "bounds", a_flags: "属性",
  },
};
const lang = (navigator.language || "en").slice(0, 2) in STRINGS ? (navigator.language || "en").slice(0, 2) : "en";
const t = (key, vars = {}) => (STRINGS[lang][key] ?? STRINGS.en[key] ?? key).replace(/\{(\w+)\}/g, (_, k) => String(vars[k] ?? ""));
document.documentElement.lang = lang;
for (const el of document.querySelectorAll("[data-t]")) el.textContent = t(el.dataset.t);

const $ = (id) => document.getElementById(id);
const store = {
  get(k, d) { try { return localStorage.getItem(k) ?? d; } catch { return d; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* private window */ } },
};

const state = { devices: [], device: "", snap: null, flat: [], picked: -1, cmd: "", code: store.get("dl-lang", "python"), rec: null };

async function api(path, body) {
  const res = await fetch(path, body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

let toastTimer;
function toast(msg, bad = false) {
  const el = $("toast");
  el.textContent = msg;
  el.classList.toggle("bad", bad);
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (el.hidden = true), bad ? 6000 : 1800);
}

// Theme: auto follows the system; the choice is kept per browser.
const themes = ["auto", "light", "dark"];
function applyTheme(name) {
  if (name === "auto") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = name;
  $("theme-label").textContent = t(`theme_${name}`);
}
let theme = store.get("dl-theme", "auto");
applyTheme(theme);
$("theme").addEventListener("click", () => {
  theme = themes[(themes.indexOf(theme) + 1) % themes.length];
  store.set("dl-theme", theme);
  applyTheme(theme);
});

// --- Devices ---
async function loadDevices() {
  const sel = $("device");
  try {
    state.devices = await api("/api/devices");
  } catch (e) {
    showScreenState(e.message, true);
    return;
  }
  sel.innerHTML = "";
  for (const d of state.devices) {
    const o = document.createElement("option");
    o.value = d.id;
    o.textContent = d.online ? `${d.name} (${d.model})` : `${d.name} (${t("offline")})`;
    o.disabled = !d.online;
    sel.append(o);
  }
  const online = state.devices.filter((d) => d.online);
  if (!online.length) {
    showScreenState(t("no_phone"));
    $("refresh").disabled = true;
    return;
  }
  const saved = store.get("dl-device", "");
  state.device = online.some((d) => d.id === saved) ? saved : online[0].id;
  sel.value = state.device;
  await refresh();
  await loadRecord();
}
$("device").addEventListener("change", async (e) => {
  state.device = e.target.value;
  store.set("dl-device", state.device);
  await refresh();
  await loadRecord();
});

// --- Screen and tree ---
function showScreenState(msg, error = false) {
  const el = $("screen-state");
  el.textContent = msg;
  el.classList.toggle("error", error);
  el.hidden = !msg;
}

async function refresh(settle = false) {
  if (!state.device) return;
  const btn = $("refresh");
  btn.disabled = true;
  showScreenState(t("reading"));
  try {
    const snap = await api("/api/snapshot", { device: state.device, settle });
    state.snap = snap;
    renderSnapshot();
    showScreenState("");
  } catch (e) {
    showScreenState(e.message, true);
  } finally {
    btn.disabled = false;
  }
}
$("refresh").addEventListener("click", () => refresh());

function flatten(node, depth, out) {
  out.push({ node, depth });
  for (const c of node.children || []) flatten(c, depth + 1, out);
  return out;
}

const pct = (v, total) => `${(v / total) * 100}%`;
function place(box, b) {
  const { width, height } = state.snap;
  Object.assign(box.style, { left: pct(b[0], width), top: pct(b[1], height), width: pct(b[2] - b[0], width), height: pct(b[3] - b[1], height) });
  box.hidden = false;
}

function label(n) {
  return n.text || n.desc || (n.id ? n.id.split(":id/").pop() : "");
}

function renderSnapshot() {
  const s = state.snap;
  $("where").textContent = [s.package, s.activity].filter(Boolean).join(" / ");
  state.flat = flatten(s.tree, 0, []);
  state.picked = -1;
  const screen = $("screen");
  const img = $("shot");
  const outlines = $("outlines");
  outlines.innerHTML = "";
  screen.hidden = false;
  if (s.image) {
    img.src = s.image;
    img.hidden = false;
    screen.classList.remove("blank");
    $("shot-note").hidden = true;
  } else {
    img.removeAttribute("src");
    img.hidden = true;
    screen.classList.add("blank");
    screen.style.setProperty("--ratio", `${s.width} / ${s.height}`);
    for (const { node } of state.flat) {
      const b = node.bounds || [0, 0, 0, 0];
      if (b[2] <= b[0] || b[3] <= b[1]) continue;
      const span = document.createElement("span");
      Object.assign(span.style, { left: pct(b[0], s.width), top: pct(b[1], s.height), width: pct(b[2] - b[0], s.width), height: pct(b[3] - b[1], s.height) });
      outlines.append(span);
    }
    const note = $("shot-note");
    note.textContent = t("no_shot", { err: s.image_error || "" });
    note.hidden = false;
  }
  $("hover-box").hidden = true;
  $("pick-box").hidden = true;
  renderTree();
  $("el-empty").hidden = false;
  $("el-body").hidden = true;
}

function renderTree() {
  const tree = $("tree");
  tree.innerHTML = "";
  const frag = document.createDocumentFragment();
  for (const { node, depth } of state.flat) {
    const b = document.createElement("button");
    b.className = "node";
    b.setAttribute("role", "treeitem");
    b.setAttribute("aria-selected", "false");
    b.style.setProperty("--depth", depth);
    b.dataset.i = node._i;
    const cls = document.createElement("span");
    cls.className = "cls";
    cls.textContent = (node.class || "?").split(".").pop();
    const txt = document.createElement("span");
    txt.className = "txt";
    txt.textContent = label(node);
    b.append(cls, txt);
    const flags = ["clickable", "editable", "scrollable", "checked"].filter((f) => node[f]);
    if (flags.length) {
      const f = document.createElement("span");
      f.className = "flag";
      f.textContent = flags.join(" ");
      b.append(f);
    }
    frag.append(b);
  }
  tree.append(frag);
}

$("tree").addEventListener("click", (e) => {
  const row = e.target.closest(".node");
  if (row) pick(Number(row.dataset.i), false);
});
$("tree").addEventListener("keydown", (e) => {
  if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
  const rows = [...$("tree").querySelectorAll(".node")];
  const i = rows.indexOf(document.activeElement);
  const next = rows[i + (e.key === "ArrowDown" ? 1 : -1)];
  if (next) {
    e.preventDefault();
    next.focus();
  }
});

// The element a tap at this point would hit: the smallest one containing it.
function nodeAt(x, y) {
  let best = null;
  let area = Infinity;
  for (const { node } of state.flat) {
    const b = node.bounds || [0, 0, 0, 0];
    if (x < b[0] || x >= b[2] || y < b[1] || y >= b[3]) continue;
    const a = (b[2] - b[0]) * (b[3] - b[1]);
    if (a <= area) {
      best = node;
      area = a;
    }
  }
  return best;
}

function pointToScreen(e) {
  const r = $("screen").getBoundingClientRect();
  return [((e.clientX - r.left) / r.width) * state.snap.width, ((e.clientY - r.top) / r.height) * state.snap.height];
}
$("screen").addEventListener("pointermove", (e) => {
  if (!state.snap) return;
  const n = nodeAt(...pointToScreen(e));
  if (n) place($("hover-box"), n.bounds);
});
$("screen").addEventListener("pointerleave", () => ($("hover-box").hidden = true));
$("screen").addEventListener("click", (e) => {
  if (!state.snap) return;
  const n = nodeAt(...pointToScreen(e));
  if (n) pick(n._i, true);
});

function pick(i, fromScreen) {
  state.picked = i;
  const entry = state.flat.find((f) => f.node._i === i);
  if (!entry) return;
  const n = entry.node;
  for (const row of $("tree").querySelectorAll('.node[aria-selected="true"]')) row.setAttribute("aria-selected", "false");
  const row = $("tree").querySelector(`.node[data-i="${i}"]`);
  if (row) {
    row.setAttribute("aria-selected", "true");
    if (fromScreen) row.scrollIntoView({ block: "nearest" });
  }
  place($("pick-box"), n.bounds);
  renderAttrs(n);
  state.cmd = "";
  suggest();
  selectTab("el");
}

function renderAttrs(n) {
  const dl = $("attrs");
  dl.innerHTML = "";
  const add = (k, v) => {
    if (v === "" || v === undefined) return;
    const dt = document.createElement("dt");
    dt.textContent = t(`a_${k}`);
    const dd = document.createElement("dd");
    if (v instanceof Node) dd.append(v);
    else dd.textContent = v;
    dl.append(dt, dd);
  };
  add("text", n.text);
  add("id", n.id);
  add("desc", n.desc);
  add("class", n.class);
  add("package", n.package);
  add("bounds", JSON.stringify(n.bounds));
  const flags = ["clickable", "long_clickable", "checkable", "checked", "enabled", "focused", "selected", "scrollable", "editable", "password"].filter((f) => n[f]);
  if (n.visible === false) flags.push("visible: false");
  if (flags.length) {
    const box = document.createElement("div");
    box.className = "chips";
    for (const f of flags) {
      const c = document.createElement("span");
      c.className = "chip";
      c.textContent = f;
      box.append(c);
    }
    add("flags", box);
  }
  $("el-empty").hidden = true;
  $("el-body").hidden = false;
}

// --- Selectors and code ---
function pressed(groupId, attr, value) {
  for (const b of $(groupId).querySelectorAll("button")) b.setAttribute("aria-pressed", String(b.dataset[attr] === value));
}

async function suggest() {
  const list = $("selectors");
  list.innerHTML = "";
  try {
    const res = await api("/api/suggest", { snap: state.snap.snap, index: state.picked, cmd: state.cmd || undefined });
    state.cmd = res.cmd;
    state.suggestions = res.selectors || [];
    pressed("cmds", "cmd", state.cmd);
    renderSelectors();
  } catch (e) {
    toast(e.message, true);
  }
}

function selectorText(s) {
  return typeof s.by === "string" ? `${s.by} = ${s.value}` : JSON.stringify(s.by);
}

function renderSelectors() {
  pressed("langs", "lang", state.code);
  const list = $("selectors");
  list.innerHTML = "";
  for (const item of state.suggestions) {
    const s = item.selector;
    const li = document.createElement("li");
    li.className = "sel";
    const head = document.createElement("div");
    head.className = "sel-head";
    const name = document.createElement("code");
    name.textContent = selectorText(s);
    const match = document.createElement("span");
    match.className = `match ${s.count === 1 ? "unique" : "shared"}`;
    match.textContent = s.count === 1 ? t("unique") : t("shared", { n: s.count, i: s.nth });
    head.append(name, match);
    const pre = document.createElement("pre");
    const code = document.createElement("code");
    code.textContent = item.code[state.code];
    pre.append(code);
    const actions = document.createElement("div");
    actions.className = "sel-actions";
    const copy = document.createElement("button");
    copy.className = "copy";
    copy.textContent = t("copy");
    copy.addEventListener("click", () => copyText(item.code[state.code]));
    actions.append(copy);
    if (state.cmd !== "input") {
      const tryBtn = document.createElement("button");
      tryBtn.className = "try";
      tryBtn.textContent = t("try");
      tryBtn.addEventListener("click", () => tryStep(s, tryBtn));
      actions.append(tryBtn);
    }
    li.append(head, pre, actions);
    list.append(li);
  }
}

async function tryStep(s, btn) {
  btn.disabled = true;
  try {
    const res = await api("/api/run", { device: state.device, cmd: state.cmd, by: s.by, value: s.value, nth: s.nth });
    toast(res.value !== undefined ? `${t("ran")}: ${JSON.stringify(res.value)}` : t("ran"));
    if (state.cmd === "touch" || state.cmd === "long_touch") refresh(true);
  } catch (e) {
    toast(e.message, true);
  } finally {
    btn.disabled = false;
  }
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast(t("copied"));
  } catch {
    toast(text);
  }
}

$("cmds").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b || state.picked < 0) return;
  state.cmd = b.dataset.cmd;
  suggest();
});
for (const id of ["langs", "rec-langs"]) {
  $(id).addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    state.code = b.dataset.lang;
    store.set("dl-lang", state.code);
    if (state.suggestions) renderSelectors();
    renderRecord();
  });
}

// --- Tabs ---
function selectTab(which) {
  const el = which === "el";
  $("tab-el").setAttribute("aria-selected", String(el));
  $("tab-rec").setAttribute("aria-selected", String(!el));
  $("pane-el").hidden = !el;
  $("pane-rec").hidden = el;
}
$("tab-el").addEventListener("click", () => selectTab("el"));
$("tab-rec").addEventListener("click", () => selectTab("rec"));

// --- Recorder ---
async function loadRecord() {
  try {
    state.rec = await api(`/api/record?device=${encodeURIComponent(state.device)}`);
  } catch {
    state.rec = null;
  }
  renderRecord();
}

function renderRecord() {
  pressed("rec-langs", "lang", state.code);
  const v = state.rec;
  const on = Boolean(v && v.on);
  const toggle = $("rec-toggle");
  toggle.textContent = on ? t("rec_stop") : t("rec_start");
  toggle.setAttribute("aria-pressed", String(on));
  $("rec-dot").hidden = !on;
  const steps = (v && v.steps) || [];
  $("rec-empty").hidden = steps.length > 0;
  $("rec-wrap").hidden = steps.length === 0;
  $("rec-code").textContent = steps.length ? v.scripts[state.code] : "";
}

$("rec-toggle").addEventListener("click", async () => {
  const on = !(state.rec && state.rec.on);
  try {
    state.rec = await api("/api/record", { device: state.device, on });
    renderRecord();
  } catch (e) {
    toast(e.message, true);
  }
});
$("rec-clear").addEventListener("click", async () => {
  try {
    state.rec = await api("/api/record/clear", { device: state.device });
    renderRecord();
  } catch (e) {
    toast(e.message, true);
  }
});
document.querySelector('[data-copy="rec-code"]').addEventListener("click", () => copyText($("rec-code").textContent));

const events = new EventSource("/api/events");
events.onmessage = (e) => {
  const v = JSON.parse(e.data);
  if (v.device === state.device) {
    state.rec = v;
    renderRecord();
  }
};

loadDevices();
