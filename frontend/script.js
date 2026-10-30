// URL del gateway (nginx). El proposito en este proyecto es que sea un scanner local.

const API = "http://localhost:8080";

// Módulos disponibles y el tópico de Kafka que usa cada uno.

const MODULES = {
  port_scan: { name: "Port Scan", topic: "scan.port_scan" },
  http_scan: { name: "HTTP Scan", topic: "scan.http_scan" },
  tls_scan:  { name: "TLS Scan",  topic: "scan.tls_scan" },
};

const targetInput = document.getElementById("target");
const scanBtn = document.getElementById("scanBtn");
const pipeline = document.getElementById("pipeline");
const jobIdLabel = document.getElementById("jobId");
const history = document.getElementById("history");
const gateway = document.getElementById("gateway");

scanBtn.addEventListener("click", runScan);
checkGateway();
setInterval(checkGateway, 10000);

// Crea un elemento con clase y texto (textContent: nunca interpreta HTML, evita XSS con lo que devuelven los servicios).

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// Indicador del gateway: 'no-cors' basta para saber si el nginx responde.

async function checkGateway() {
  try {
    await fetch(API + "/health", { mode: "no-cors" });
    gateway.textContent = "Gateway en línea";
    gateway.className = "pill pill-ok";
  } catch {
    gateway.textContent = "Gateway caído";
    gateway.className = "pill pill-down";
  }
}

function setBadge(badge, status) {
  const labels = { queued: "En cola", done: "Procesado", failed: "Sin respuesta" };
  badge.textContent = labels[status];
  badge.className = "badge badge-" + status;
}

async function runScan() {
  const target = targetInput.value.trim();
  const modules = Array.from(document.querySelectorAll("input[type=checkbox]:checked")).map(c => c.value);
  if (!target || modules.length === 0) return;

  scanBtn.disabled = true;
  const started = Date.now();

  try {
    // 1. Encolar: el middleware publica en Kafka y responde 202 con el job_id.
    const res = await fetch(API + "/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ target, modules }),
    });
    if (!res.ok) throw new Error(await res.text());
    const { job_id } = await res.json();

    // 2. Dibujar un paso "En cola" por módulo y una fila en el historial.
    jobIdLabel.textContent = "job " + job_id;
    pipeline.className = "";
    pipeline.replaceChildren();
    const steps = {};
    for (const m of modules) {
      const step = el("div", "step");
      const head = el("div", "step-head");
      const title = el("span", "step-title", MODULES[m].name);
      title.append(el("code", "", MODULES[m].topic));
      const badge = el("span");
      setBadge(badge, "queued");
      head.append(title, badge);
      const meta = el("div", "meta", "Esperando a un consumidor del grupo " + m + "…");
      step.append(head, meta);
      pipeline.append(step);
      steps[m] = { step, badge, meta };
    }
    const row = addHistoryRow(job_id, target, modules);

    // 3. Consultar resultados hasta tener uno por módulo (máx. 30 s).
    let done = 0;
    for (let i = 0; i < 40 && done < modules.length; i++) {
      await new Promise(r => setTimeout(r, 750));
      const results = await (await fetch(API + "/jobs/" + job_id)).json();
      for (const r of results) {
        const s = steps[r.module];
        if (!s || s.done) continue;
        s.done = true;
        done++;
        setBadge(s.badge, "done");
        s.meta.textContent = `Procesado por ${r.worker} · partición ${r.partition || 0} · offset ${r.offset || 0}`;
        s.step.append(el("pre", "output mono", r.output));
      }
    }

    // 4. Cerrar: marcar como fallidos los módulos que nunca respondieron.
    for (const m of modules) {
      if (!steps[m].done) {
        setBadge(steps[m].badge, "failed");
        steps[m].meta.textContent = "Ningún servicio procesó el trabajo a tiempo.";
      }
    }
    const seconds = ((Date.now() - started) / 1000).toFixed(1) + " s";
    row.status.className = done === modules.length ? "badge badge-done" : "badge badge-failed";
    row.status.textContent = done === modules.length ? "Completado" : `${done}/${modules.length}`;
    row.time.textContent = seconds;

  } catch (error) {
    pipeline.className = "empty";
    pipeline.textContent = "Error: " + error.message;
  } finally {
    scanBtn.disabled = false;
  }
}

// Agrega una fila al historial (la más reciente arriba) y devuelve las celdas que se actualizan al terminar.

function addHistoryRow(jobId, target, modules) {
  if (history.querySelector("td[colspan]")) history.replaceChildren();
  const tr = el("tr");
  const status = el("span", "badge badge-queued", "En cola");
  const statusCell = el("td");
  statusCell.append(status);
  const time = el("td", "", "…");
  tr.append(el("td", "mono", jobId), el("td", "", target), el("td", "", modules.map(m => MODULES[m].name).join(", ")), statusCell, time);
  history.prepend(tr);
  return { status, time };
}
