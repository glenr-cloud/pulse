// pulse/web — the page people look at.
//
// It knows one thing about its surroundings: API_URL, the address of the api
// service. On Glenr that comes from a `type: service` connection; locally it
// comes from `docker compose` or your shell.
const http = require("node:http");

const PORT = process.env.PORT || 8080;
// On Glenr, `connects: [api]` injects API_HOST and API_PORT — the platform's
// service-discovery convention. API_URL stays as the compose/local override.
const API_URL =
  process.env.API_URL ||
  (process.env.API_HOST ? `http://${process.env.API_HOST}:${process.env.API_PORT || 80}` : "http://localhost:3000");

async function fetchPings() {
  const res = await fetch(`${API_URL}/pings`, { signal: AbortSignal.timeout(3000) });
  if (!res.ok) throw new Error(`api answered ${res.status}`);
  return res.json();
}

function page(data, err) {
  const rows = (data?.pings ?? [])
    .slice(-25)
    .reverse()
    .map((p) => `<tr><td>${p.from}</td><td>${p.at}</td><td>${p.note ?? ""}</td></tr>`)
    .join("");
  return `<!doctype html><meta charset="utf-8"><title>pulse</title>
<style>body{font:15px/1.5 system-ui;margin:2rem auto;max-width:60rem;padding:0 1rem}
table{border-collapse:collapse;width:100%}td,th{border-bottom:1px solid #ddd;padding:.4rem .6rem;text-align:left}
.err{color:#b00}</style>
<h1>pulse</h1>
<p>api at <code>${API_URL}</code> — ${err ? `<span class="err">${err}</span>` : `${data.count} ping(s) recorded`}</p>
<table><tr><th>from</th><th>at (UTC)</th><th>note</th></tr>${rows}</table>`;
}

http.createServer(async (req, res) => {
  if (req.url === "/healthz") { res.writeHead(200); return res.end("ok\n"); }
  try {
    const data = await fetchPings();
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    res.end(page(data));
  } catch (e) {
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    res.end(page(null, `cannot reach the api: ${e.message}`));
  }
}).listen(PORT, () => console.log(`pulse/web on :${PORT}, api at ${API_URL}`));
// demo walk (demo-dev 2026-10-29T09:23:12.367Z)
