"use strict";
// The workflow editor. The token arrives in the URL fragment (never sent to the
// server) and is kept for this tab only, so a reload works and the address bar
// stays clean. Navigation lives in ?w=<workflow>&n=<node>, so Back works.
const q0 = new URLSearchParams(location.search);
let token = location.hash.slice(1);
try {
  if (token) sessionStorage.setItem("pie-token", token); else token = sessionStorage.getItem("pie-token") || "";
} catch (e) {}
if (location.hash) history.replaceState(null, "", location.pathname + location.search);

const S = {repos: [], repo: "", file: "", slots: [], byId: {}, saved: {}, draft: {}, stages: [], workflows: [],
  lint: {}, w: q0.get("w"), n: q0.get("n"), pending: false};
// ?stage=<id> (older links): open the first workflow that has that stage.
const legacyStage = q0.get("stage");

const $ = (sel, el = document) => el.querySelector(sel);
const esc = s => String(s).replace(/[&<>"']/g, c => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"}[c]));
const norm = s => String(s || "").replace(/\r\n/g, "\n").replace(/^\n+/, "").replace(/[ \t\n]+$/, "");
const GLYPH = {agent: "◉", you: "◆", auto: "▢", start: "●"};

async function api(method, path, body) {
  const r = await fetch(path, {method, headers: {"X-Pie-Token": token, "Content-Type": "application/json"},
    body: body ? JSON.stringify(body) : undefined});
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || (r.status === 401 ? "This editor link has expired - open it again from Apple Pie." : r.statusText));
  return data;
}

// Slot state: value is what the editor holds, savedVal what's on disk.
const value = id => (id in S.draft ? S.draft[id] : (S.saved[id] ?? S.byId[id].default));
const savedVal = id => S.saved[id] ?? S.byId[id].default;
const isCustom = id => norm(value(id)) !== norm(S.byId[id].default) && norm(value(id)) !== "";
const isDirty = id => norm(value(id)) !== norm(savedVal(id));
const stageSlots = st => S.slots.filter(s => s.stage === st);
const stage = id => S.stages.find(s => s.id === id);
const flow = id => S.workflows.find(w => w.id === id);
const flowSlots = w => S.slots.filter(s => w.nodes.some(n => n.stage === s.stage));
const dirtyCount = () => S.slots.filter(s => isDirty(s.id)).length;
const usedIn = st => S.workflows.filter(w => w.nodes.some(n => n.stage === st)).map(w => w.title);

function toast(msg, err) {
  const t = $("#toast");
  t.textContent = msg; t.className = "toast show" + (err ? " err" : "");
  clearTimeout(toast.t); toast.t = setTimeout(() => t.className = "toast", 2600);
}

async function load(repo) {
  const st = await api("GET", "/api/state?repo=" + encodeURIComponent(repo || ""));
  Object.assign(S, {repos: st.repos, repo: st.repo, file: st.file, slots: st.slots, saved: st.saved || {},
    stages: st.stages, workflows: st.workflows, lint: st.lint || {}, draft: {}});
  S.byId = Object.fromEntries(S.slots.map(s => [s.id, s]));
  if (legacyStage && !S.w) {
    const w = S.workflows.find(w => w.nodes.some(n => n.stage === legacyStage));
    if (w) { S.w = w.id; S.n = w.nodes.find(n => n.stage === legacyStage).id; }
  }
  $("#repo").innerHTML = S.repos.map(r => `<option ${r === S.repo ? "selected" : ""} value="${esc(r)}" title="${esc(r)}">${esc(r.split("/").filter(Boolean).pop() || r)}</option>`).join("");
  $("#file").textContent = S.file;
  render();
}

// go navigates (and records it, so Back returns here).
function go(w, n) {
  S.w = w; S.n = n;
  const qs = w ? "?w=" + encodeURIComponent(w) + (n ? "&n=" + encodeURIComponent(n) : "") : "";
  history.pushState(null, "", location.pathname + qs);
  render();
  scrollTo({top: 0, behavior: "smooth"});
}
addEventListener("popstate", () => {
  const q = new URLSearchParams(location.search);
  S.w = q.get("w"); S.n = q.get("n"); render();
});

function render() {
  const w = S.w && flow(S.w);
  if (!w) renderHome(); else renderFlow(w);
  renderChrome();
}

function renderChrome() {
  const n = dirtyCount(), el = $("#status");
  el.className = "status " + (n ? "dirty" : "saved");
  el.innerHTML = n ? `<b>●</b> ${n} unsaved change${n > 1 ? "s" : ""}` : "<b>●</b> saved";
  $("#save").disabled = !n; $("#discard").disabled = !n;
  document.querySelectorAll("[data-wcount]").forEach(paintFlowCount);
  document.querySelectorAll(".node[data-stage]").forEach(paintNode);
}

// ---- home: one card per workflow -------------------------------------------

function renderHome() {
  $("#view").innerHTML = `<h1>Workflows</h1>
    <p class="lede">Each workflow is a chain of steps. Open one to see its graph, then open an agent step (◉) to read its whole prompt and rewrite the parts your team wants different. The contract Apple Pie reads back (🔒) stays as it is.</p>
    <div class="cards">${S.workflows.map(w => {
      const main = w.nodes.filter(n => n.row === 0).sort((a, b) => a.col - b.col);
      return `<button class="card" data-w="${w.id}"><h2>${esc(w.title)}</h2><p>${esc(w.blurb)}</p>
        <div class="chain">${main.map(n => `<i class="${n.kind}">${GLYPH[n.kind]} ${esc(n.title)}</i>`).join("<b>→</b>")}</div>
        <div class="cmeta" data-wcount="${w.id}"></div></button>`;
    }).join("")}</div>`;
  document.querySelectorAll(".card").forEach(c => c.onclick = () => go(c.dataset.w, null));
}

function paintFlowCount(el) {
  const w = flow(el.dataset.wcount), sl = flowSlots(w);
  const prompts = new Set(w.nodes.filter(n => n.stage).map(n => n.stage)).size;
  const c = sl.filter(s => isCustom(s.id)).length, d = sl.filter(s => isDirty(s.id)).length;
  el.innerHTML = `<span>${prompts} agent prompt${prompts > 1 ? "s" : ""}</span>` +
    (c ? `<span class="c">● ${c} customized</span>` : "<span>default</span>") + (d ? `<span class="d">● ${d} unsaved</span>` : "");
}

// ---- a workflow: graph + the selected node --------------------------------

// Grid geometry. Columns spread to the space there is (never tighter than
// MINCW, so edge labels fit between boxes); loops back over the top get room.
const RH = 128, NW = 120, NH = 80, PAD = 26, MINCW = 166, MAXCW = 230;
let CW = MINCW, TOP = PAD;
const pos = n => ({x: PAD + n.col * CW, y: TOP + n.row * RH});

function renderFlow(w) {
  const node = w.nodes.find(n => n.id === S.n) || w.nodes.find(n => n.kind === "agent");
  S.n = node.id;
  const cols = Math.max(...w.nodes.map(n => n.col)) + 1, rows = Math.max(...w.nodes.map(n => n.row)) + 1;
  const v = $("#view"), cs = getComputedStyle(v);
  const room = (v.clientWidth || 1200) - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight) - 4;
  CW = cols > 1 ? Math.max(MINCW, Math.min(MAXCW, (room - PAD * 2 - NW) / (cols - 1))) : MINCW;
  const nodeAt = id => w.nodes.find(n => n.id === id);
  TOP = w.edges.some(e => nodeAt(e.from).row === nodeAt(e.to).row && nodeAt(e.to).col < nodeAt(e.from).col) ? 78 : PAD;
  const W = PAD * 2 + (cols - 1) * CW + NW, H = TOP + PAD + (rows - 1) * RH + NH;
  $("#view").innerHTML = `<div class="crumbs"><a data-home>Workflows</a> / ${esc(w.title)}</div>
    <h1>${esc(w.title)}</h1><p class="lede">${esc(w.blurb)}</p>
    <div class="graph"><div class="gcanvas" style="width:${W}px;height:${H}px">
      <svg width="${W}" height="${H}"><defs><marker id="ar" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" fill="#5A6577"/></marker>
      <marker id="arb" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" fill="#62B8E8"/></marker></defs>
      ${w.edges.map(e => edge(w, e)).join("")}</svg>
      ${w.nodes.map(n => { const p = pos(n); return `<button class="node ${n.kind} ${n.id === node.id ? "on" : ""}" data-node="${n.id}" ${n.stage ? `data-stage="${n.stage}"` : ""} style="left:${p.x}px;top:${p.y}px">
        <span class="nt"><span class="g">${GLYPH[n.kind]}</span>${esc(n.title)}</span>${n.note ? `<span class="ns">${esc(n.note)}</span>` : ""}<span class="tag" hidden></span></button>`; }).join("")}
    </div></div><section class="stage" id="stage"></section>`;
  $("[data-home]").onclick = () => go(null, null);
  document.querySelectorAll(".node").forEach(b => b.onclick = () => go(w.id, b.dataset.node));
  const g = $(".graph"), on = $(".node.on");
  if (on && g.scrollWidth > g.clientWidth) g.scrollLeft = on.offsetLeft - g.clientWidth / 2 + NW / 2;
  renderNode(node);
}

// edge draws one arrow between node boxes, with its label at the midpoint.
function edge(w, e) {
  const a = w.nodes.find(n => n.id === e.from), b = w.nodes.find(n => n.id === e.to), A = pos(a), B = pos(b);
  let p0, p1, p2, p3;
  if (a.row === b.row && b.col > a.col) {            // straight on
    p0 = [A.x + NW, A.y + NH / 2]; p3 = [B.x, B.y + NH / 2]; p1 = [p0[0] + 20, p0[1]]; p2 = [p3[0] - 20, p3[1]];
  } else if (a.row === b.row) {                      // loop back, over the top
    const lift = 34 + 8 * (a.col - b.col);
    p0 = [A.x + NW / 2, A.y]; p3 = [B.x + NW / 2, B.y]; p1 = [p0[0], p0[1] - lift]; p2 = [p3[0], p3[1] - lift];
  } else if (a.col === b.col) {                      // straight down or up
    const dx = b.row > a.row ? -22 : 22;
    p0 = [A.x + NW / 2 + dx, b.row > a.row ? A.y + NH : A.y]; p3 = [B.x + NW / 2 + dx, b.row > a.row ? B.y : B.y + NH];
    p1 = p0; p2 = p3;
  } else if (b.row > a.row && b.col > a.col) {        // down and on: leave from below
    p0 = [A.x + NW - 18, A.y + NH]; p3 = [B.x, B.y + NH / 2]; p1 = [p0[0], p0[1] + 46]; p2 = [p3[0] - 46, p3[1]];
  } else {                                            // across rows
    const right = b.col > a.col, down = b.row > a.row;
    p0 = [right ? A.x + NW : A.x, A.y + NH / 2]; p3 = [B.x + NW / 2, down ? B.y : B.y + NH];
    p1 = [p0[0] + (right ? 50 : -50), p0[1]]; p2 = [p3[0], p3[1] + (down ? -50 : 50)];
  }
  const d = `M${p0}C${p1} ${p2} ${p3}`;
  const mx = (p0[0] + 3 * p1[0] + 3 * p2[0] + p3[0]) / 8, my = (p0[1] + 3 * p1[1] + 3 * p2[1] + p3[1]) / 8;
  const label = e.label ? `<rect class="elabel-bg" x="${mx - e.label.length * 3.1 - 5}" y="${my - 8}" width="${e.label.length * 6.2 + 10}" height="15" rx="7"/>
    <text class="elabel" x="${mx}" y="${my + 3}" text-anchor="middle">${esc(e.label)}</text>` : "";
  return `<path class="edge ${e.back ? "back" : ""}" d="${d}" marker-end="url(#${e.back ? "arb" : "ar"})"/>${label}`;
}

function paintNode(el) {
  const st = stage(el.dataset.stage), sl = stageSlots(el.dataset.stage), tag = $(".tag", el);
  const c = sl.filter(s => isCustom(s.id)).length, d = sl.some(s => isDirty(s.id));
  tag.hidden = !(c || d || st.fixed);
  tag.className = "tag" + (d ? " d" : st.fixed && !c ? " f" : "");
  tag.textContent = d ? "● unsaved" : c ? `${c} custom` : "fixed";
}

// ---- a node: its prompt, or what happens there ----------------------------

function renderNode(n) {
  const el = $("#stage");
  if (n.kind !== "agent") {
    const what = {start: "Where this workflow begins.", you: "Apple Pie waits for you here - nothing moves until you decide.",
      auto: "Apple Pie does this itself, with no agent, so there is no prompt to customize."}[n.kind];
    el.innerHTML = `<div class="shead"><h2>${GLYPH[n.kind]} ${esc(n.title)}</h2></div><div class="info"><b>${what}</b><br>${esc(n.note || "")}</div>`;
    return;
  }
  const st = stage(n.stage), sl = stageSlots(n.stage);
  el.innerHTML = `<div class="shead"><h2>◉ ${esc(st.title)}</h2><p class="lede">${esc(st.blurb)}</p>
    <div class="smeta" id="smeta"></div></div>
    ${st.fixed ? `<div class="notice">This prompt is fixed: it carries only your feedback and the contract around it, so there is nothing to customize. It is shown so you can see exactly what the agent receives.</div>` :
      `<div class="notice">The whole prompt the agent receives, top to bottom, for a sample ticket. Edit the ✎ boxes in place; 🔒 text is the contract and stays as it is.${sl.length > 1 ? "" : ""}</div>`}
    <div class="doc" id="doc"></div>`;
  renderDoc(st);
}

function renderDoc(st) {
  S.sig = sig(st);
  $("#doc").innerHTML = st.blocks.map(b => {
    if (!b.slot) return b.text.trim() ? `<div class="lk ${/\.agent\/|\.json/.test(b.text) ? "contract" : ""}"><pre>${esc(b.text.replace(/^\n+|\s+$/g, ""))}</pre></div>` : "";
    const s = S.byId[b.slot];
    return `<div class="ed" data-slot="${s.id}"><div class="ebar"><b>✎ ${esc(s.title)}</b><span class="badge"></span><span class="spacer"></span>
      <button class="link reset">↺ reset to default</button><button class="link undo">⟲ undo</button><span class="count"></span><span class="help">${esc(s.help)}</span></div>
      <textarea spellcheck="false" placeholder="${s.default ? "" : "Empty - add house rules, tools to prefer, things to avoid…"}">${esc(value(s.id))}</textarea><ul class="lint"></ul></div>`;
  }).join("");
  document.querySelectorAll(".ed").forEach(card => {
    const id = card.dataset.slot, ta = $("textarea", card);
    const fit = () => { ta.style.height = "auto"; ta.style.height = ta.scrollHeight + 2 + "px"; };
    ta.oninput = () => { S.draft[id] = ta.value; fit(); paintSlot(card); renderChrome(); refresh(); };
    ta.onblur = () => { if (S.pending) setTimeout(() => { if (!document.activeElement?.matches?.("textarea")) rerender(); }, 0); };
    $(".reset", card).onclick = () => { ta.value = S.byId[id].default; ta.oninput(); };
    $(".undo", card).onclick = () => { delete S.draft[id]; ta.value = value(id); ta.oninput(); };
    requestAnimationFrame(fit);
    paintSlot(card);
  });
  paintMeta(st);
}

const sig = st => st.blocks.map(b => b.slot ? "S" + b.slot : "L" + b.text).join("\u0000");

function paintMeta(st) {
  const text = st.blocks.map(b => b.text).join(""), c = stageSlots(st.id).filter(s => isCustom(s.id)).length;
  const m = $("#smeta");
  if (m) m.innerHTML = `<span><i>${text.split("\n").length}</i> lines</span><span><i>${text.length.toLocaleString()}</i> chars</span>
    <span>≈ <i>${Math.round(text.length / 4).toLocaleString()}</i> tokens</span><span><i>${c}</i> customized</span><span>used in: <i>${esc(usedIn(st.id).join(", "))}</i></span>`;
}

function paintSlot(card) {
  const id = card.dataset.slot, c = isCustom(id), d = isDirty(id), v = value(id);
  card.classList.toggle("custom", c); card.classList.toggle("dirty", d);
  $(".badge", card).textContent = d ? "● unsaved" : c ? "● customized" : (S.byId[id].default ? "default" : "empty");
  $(".count", card).textContent = `${v.length.toLocaleString()} / 8,192`;
  $(".reset", card).disabled = norm(v) === norm(S.byId[id].default);
  $(".undo", card).disabled = !d;
  $(".lint", card).innerHTML = (S.lint[id] || []).map(w => `<li>${esc(w)}</li>`).join("");
}

// rerender redraws the open prompt when its shape changed (team instructions
// gained or lost their heading) - never while you are typing in it.
function rerender() {
  S.pending = false;
  const w = S.w && flow(S.w), n = w && w.nodes.find(x => x.id === S.n);
  if (n && n.stage) renderDoc(stage(n.stage));
}

function currentSlots() {
  const out = {};
  for (const s of S.slots) if (isCustom(s.id)) out[s.id] = norm(value(s.id));
  return out;
}

// refresh rebuilds every prompt from the real builders (debounced).
function refresh() {
  clearTimeout(refresh.t);
  refresh.t = setTimeout(async () => {
    try {
      const r = await api("POST", "/api/preview", {repo: S.repo, slots: currentSlots()});
      S.stages = r.stages; S.lint = r.lint || {};
      const w = S.w && flow(S.w), n = w && w.nodes.find(x => x.id === S.n);
      if (!n || !n.stage) return;
      const st = stage(n.stage);
      document.querySelectorAll(".ed").forEach(paintSlot);
      paintMeta(st);
      if (sig(st) !== S.sig) {
        if (document.activeElement && document.activeElement.matches("textarea")) S.pending = true; else rerender();
      }
    } catch (err) { toast(err.message, true); }
  }, 220);
}

async function save() {
  if (!dirtyCount()) return;
  try {
    const st = await api("PUT", "/api/workflows", {repo: S.repo, slots: currentSlots()});
    Object.assign(S, {saved: st.saved || {}, stages: st.stages, lint: st.lint || {}, draft: {}});
    render();
    toast("Saved - the next run of " + S.repo.split("/").pop() + " uses this workflow");
  } catch (err) { toast(err.message, true); }
}

$("#save").onclick = save;
$("#discard").onclick = () => { S.draft = {}; refresh(); render(); };
$("#home").onclick = () => go(null, null);
$("#repo").onchange = async e => {
  if (dirtyCount() && !confirm("Discard unsaved changes to this repo's workflow?")) { e.target.value = S.repo; return; }
  await load(e.target.value).catch(err => toast(err.message, true));
};
addEventListener("keydown", e => { if ((e.metaKey || e.ctrlKey) && e.key === "s") { e.preventDefault(); save(); } });
addEventListener("beforeunload", e => { if (dirtyCount()) { e.preventDefault(); e.returnValue = ""; } });

load("").catch(err => { $("#view").innerHTML = `<div class="boot err">${esc(err.message)}</div>`; });
