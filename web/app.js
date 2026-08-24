// CacaoFerment console: reads live backend state, locks tasks, and renders the
// full audit trail for a selected task.
const healthEl = document.getElementById("health");
const tasksEl = document.getElementById("tasks");
const lockResultEl = document.getElementById("lock-result");
const detailEl = document.getElementById("detail");
const detailIdEl = document.getElementById("detail-id");
const detailBodyEl = document.getElementById("detail-body");
const actionResultEl = document.getElementById("action-result");

let selectedTaskId = null;

async function loadHealth() {
  try {
    const res = await fetch("/api/health");
    const data = await res.json();
    healthEl.textContent = `后端状态：${data.status}`;
  } catch {
    healthEl.textContent = "后端不可达";
  }
}

function taskState(t) {
  return t.State ?? t.state ?? "?";
}

async function loadTasks() {
  try {
    const res = await fetch("/api/tasks");
    const data = await res.json();
    const tasks = data.tasks || [];
    tasksEl.innerHTML = "";
    if (tasks.length === 0) {
      const li = document.createElement("li");
      li.textContent = "暂无任务";
      tasksEl.appendChild(li);
      return;
    }
    for (const t of tasks) {
      const li = document.createElement("li");
      const a = document.createElement("a");
      a.href = "#";
      a.textContent = `${t.TaskID ?? t.task_id} — 状态 ${taskState(t)}`;
      a.addEventListener("click", (e) => {
        e.preventDefault();
        selectTask(t.TaskID ?? t.task_id);
      });
      li.appendChild(a);
      tasksEl.appendChild(li);
    }
  } catch (err) {
    tasksEl.textContent = `加载失败：${err.message}`;
  }
}

async function selectTask(id) {
  selectedTaskId = id;
  detailEl.classList.remove("hidden");
  detailIdEl.textContent = id;
  await loadAudit(id);
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

async function loadAudit(id) {
  try {
    const res = await fetch(`/api/tasks/${encodeURIComponent(id)}/audit`);
    const audit = await res.json();
    renderAudit(audit);
  } catch (err) {
    detailBodyEl.textContent = `审计加载失败：${err.message}`;
  }
}

function renderAudit(a) {
  const t = a.task || {};
  const html = [];
  html.push(`<p class="progress">当前状态：<strong>${esc(taskState(t))}</strong> · 代次 ${esc(t.Generation ?? 1)}</p>`);

  const leases = a.leases || [];
  html.push("<h3>租约</h3>");
  if (leases.length === 0) html.push("<p class='muted'>无租约</p>");
  else {
    html.push("<table><thead><tr><th>类型</th><th>资源</th><th>状态</th></tr></thead><tbody>");
    for (const l of leases) html.push(`<tr><td>${esc(l.ResourceType)}</td><td>${esc(l.ResourceID)}</td><td>${esc(l.Status)}</td></tr>`);
    html.push("</tbody></table>");
  }

  const cells = a.coverage_cells || [];
  html.push("<h3>翻堆覆盖</h3>");
  if (cells.length === 0) html.push("<p class='muted'>无覆盖读数</p>");
  else {
    html.push("<table><thead><tr><th>节点</th><th>温度(℃)</th><th>分钟</th><th>翻堆</th><th>斜率(milli/min)</th><th>有效</th></tr></thead><tbody>");
    for (const c of cells) {
      html.push(`<tr><td>${esc(c.TurnNode)}</td><td>${(c.TemperatureCentiC / 100).toFixed(2)}</td><td>${esc(c.DurationMinutes)}</td><td>${esc(c.TurnCount)}</td><td>${esc(c.SlopeMilliPerMin)}</td><td>${c.Valid ? "是" : "否"}</td></tr>`);
    }
    html.push("</tbody></table>");
  }

  const blinds = a.blind_samples || [];
  html.push("<h3>盲码样本</h3>");
  if (blinds.length === 0) html.push("<p class='muted'>无盲码</p>");
  else {
    html.push("<table><thead><tr><th>盲码</th><th>样本粒数</th><th>揭示箱位</th><th>密封</th></tr></thead><tbody>");
    for (const b of blinds) html.push(`<tr><td>${esc(b.BlindCode)}</td><td>${esc(b.SampleSize)}</td><td>${esc(b.RevealedBinID || "—")}</td><td>${b.Sealed ? "是" : "否"}</td></tr>`);
    html.push("</tbody></table>");
  }

  const reviews = a.reviews || [];
  html.push("<h3>独立复核</h3>");
  if (reviews.length === 0) html.push("<p class='muted'>无复核</p>");
  else html.push("<ul>" + reviews.map((r) => `<li>${esc(r.ReviewerID)} — ${esc(r.Decision)}</li>`).join("") + "</ul>");

  if (a.credential) {
    html.push(`<h3>终局凭据</h3><p>${esc(a.credential.TerminalState)} · ${esc(a.credential.CredentialID)}</p>`);
  }

  detailBodyEl.innerHTML = html.join("");
}

async function submitLock(event) {
  event.preventDefault();
  const form = event.target;
  const data = new FormData(form);
  const binIds = String(data.get("bin_ids") || "").split(",").map((s) => s.trim()).filter(Boolean);
  const payload = {
    rule_version: data.get("rule_version"),
    plot_id: data.get("plot_id"),
    variety_batch_id: data.get("variety_batch_id"),
    bin_ids: binIds,
    probe_ids: ["probe-1"],
    plate_well_ids: ["well-1"],
    boxed_weight_grams: Number(data.get("boxed_weight_grams")),
    drying_window_id: data.get("drying_window_id"),
    blind_samples: binIds.map((b, i) => ({ blind_code: `BC-${i + 1}`, sample_size: 100, bin_id: b })),
  };
  try {
    const res = await fetch("/api/tasks/lock", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const body = await res.json();
    lockResultEl.textContent = JSON.stringify(body, null, 2);
    await loadTasks();
  } catch (err) {
    lockResultEl.textContent = `锁定失败：${err.message}`;
  }
}

async function submitReview() {
  if (!selectedTaskId) return;
  const reviewer = document.getElementById("reviewer").value;
  const decision = document.getElementById("decision").value;
  const payload = { operation_key: `ui-rev-${Date.now()}`, generation: 1, reviewer_id: reviewer, decision };
  try {
    const res = await fetch(`/api/tasks/${selectedTaskId}/reviews`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const body = await res.json();
    actionResultEl.textContent = JSON.stringify(body, null, 2);
    await loadAudit(selectedTaskId);
  } catch (err) {
    actionResultEl.textContent = `复核失败：${err.message}`;
  }
}

async function finalizeTask() {
  if (!selectedTaskId) return;
  const payload = { operation_key: `ui-fin-${Date.now()}`, generation: 1, decision: "ready_to_dry" };
  try {
    const res = await fetch(`/api/tasks/${selectedTaskId}/finalize`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const body = await res.json();
    actionResultEl.textContent = JSON.stringify(body, null, 2);
    await loadAudit(selectedTaskId);
    await loadTasks();
  } catch (err) {
    actionResultEl.textContent = `终局失败：${err.message}`;
  }
}

document.getElementById("lock").addEventListener("submit", submitLock);
document.getElementById("btn-review").addEventListener("click", submitReview);
document.getElementById("btn-finalize").addEventListener("click", finalizeTask);

loadHealth();
loadTasks();
setInterval(loadTasks, 5000);
