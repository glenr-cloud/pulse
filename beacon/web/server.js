// beacon — the front of house.
//
// One page, server-rendered, zero npm dependencies. It talks to `api` over the
// address `connects: [api]` injects (API_HOST / API_PORT) and renders the rows
// itself, which keeps the browser same-origin and the CSP trivial.
//
// The honesty device is the HOSTNAME. This process is told nothing about which
// zone it runs in — no env var, no build arg, no label. It reads the `Host`
// header off the request the visitor's browser actually sent, and the zone
// segment in that name is placed there by the platform's own routing. If the
// badge says lz3, the packet arrived at an lz3 address.

const http = require("node:http");

const PORT = Number(process.env.PORT || 8080);
const API_HOST = process.env.API_HOST || "127.0.0.1";
const API_PORT = Number(process.env.API_PORT || 3000);

// ── The zone map ────────────────────────────────────────────────────────────
// Anything not listed renders the raw host instead of a guess, so a hostname
// this map has never seen is visibly unmapped rather than quietly mislabelled.
const ZONES = {
  lz1: "Hetzner · Nuremberg",
  lz2: "Hetzner · Helsinki",
  lz3: "Scaleway · Paris",
};

// readZone pulls the zone segment out of a platform hostname. Addresses look
// like `web--production--beacon--acme.lz3.apps.glenr.io`, so the zone is the
// first dot-separated label matching lz<digits>. Returns null when there is
// none — a bare `localhost:8080` has no zone and must not pretend to.
function readZone(host) {
  const name = String(host || "").split(":")[0];
  for (const label of name.split(".")) {
    if (/^lz\d+$/.test(label)) return label;
  }
  return null;
}

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// relativeTime keeps the list scannable on camera: an absolute timestamp is
// noise at a glance, and "4m ago" is the thing a viewer actually reads.
function relativeTime(iso) {
  const then = Date.parse(iso);
  if (!Number.isFinite(then)) return "";
  const seconds = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (seconds < 60) return seconds <= 1 ? "just now" : `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

// ── The api, spoken to server-side ──────────────────────────────────────────
// Every call is time-boxed. A hung api must degrade the page, never hang it:
// a request that never resolves is a blank tab in front of an audience.
function callAPI(method, path, body) {
  return new Promise((resolve) => {
    const payload = body === undefined ? null : Buffer.from(JSON.stringify(body));
    const request = http.request(
      {
        host: API_HOST,
        port: API_PORT,
        path,
        method,
        timeout: 2500,
        headers: payload
          ? { "content-type": "application/json", "content-length": payload.length }
          : {},
      },
      (response) => {
        const chunks = [];
        response.on("data", (chunk) => chunks.push(chunk));
        response.on("end", () => {
          const text = Buffer.concat(chunks).toString("utf8");
          let parsed = null;
          try {
            parsed = JSON.parse(text);
          } catch {
            parsed = null;
          }
          resolve({ ok: response.statusCode < 400, status: response.statusCode, body: parsed });
        });
      },
    );
    request.on("timeout", () => request.destroy(new Error("timeout")));
    request.on("error", () => resolve({ ok: false, status: 0, body: null }));
    if (payload) request.write(payload);
    request.end();
  });
}

// ── The page ────────────────────────────────────────────────────────────────

const STYLE = `
  *, *::before, *::after { box-sizing: border-box; }
  html { -webkit-text-size-adjust: 100%; }
  body {
    margin: 0;
    background: #0a0a0b;
    color: #ededed;
    font-family: Inter, ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    font-size: 16px;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
  }
  .page { max-width: 720px; margin: 0 auto; padding: 72px 28px 96px; }
  .wordmark {
    display: flex; align-items: center; gap: 10px;
    font-size: 14px; font-weight: 600; letter-spacing: 0.16em; text-transform: uppercase;
    color: #8a8a91;
  }
  .wordmark .dot {
    width: 9px; height: 9px; border-radius: 50%;
    background: #f59e0b; box-shadow: 0 0 14px 2px rgba(245, 158, 11, 0.55);
  }
  .serving { margin: 56px 0 12px; font-size: 12px; font-weight: 600; letter-spacing: 0.2em; text-transform: uppercase; color: #6f6f77; }
  .zone {
    margin: 0;
    font-size: clamp(44px, 11vw, 92px);
    font-weight: 700; letter-spacing: -0.035em; line-height: 1;
    color: #f59e0b;
  }
  .zone.unmapped { font-size: clamp(22px, 4.6vw, 34px); color: #ededed; word-break: break-all; letter-spacing: -0.02em; }
  .place { margin: 16px 0 0; font-size: 19px; color: #b4b4bb; }
  .host { margin: 10px 0 0; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 13px; color: #6f6f77; word-break: break-all; }
  .rule { height: 1px; background: #1e1e21; border: 0; margin: 52px 0; }
  form { display: flex; gap: 10px; margin: 0 0 40px; }
  input[type="text"] {
    flex: 1; min-width: 0;
    background: #131316; border: 1px solid #26262b; border-radius: 10px;
    padding: 14px 16px; color: #ededed; font: inherit; font-size: 16px;
  }
  input[type="text"]::placeholder { color: #5d5d65; }
  input[type="text"]:focus { outline: none; border-color: #f59e0b; box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.16); }
  button {
    background: #f59e0b; color: #17170c; border: 0; border-radius: 10px;
    padding: 14px 22px; font: inherit; font-weight: 650; cursor: pointer; white-space: nowrap;
  }
  button:hover { background: #fbaf24; }
  ul { list-style: none; margin: 0; padding: 0; }
  li { display: flex; align-items: baseline; gap: 18px; padding: 15px 0; border-bottom: 1px solid #161619; }
  li:last-child { border-bottom: 0; }
  .body { flex: 1; min-width: 0; font-size: 17px; overflow-wrap: anywhere; }
  .when { flex-shrink: 0; font-size: 13px; color: #6f6f77; font-variant-numeric: tabular-nums; }
  .empty { color: #6f6f77; font-size: 16px; padding: 18px 0; }
  footer { margin-top: 60px; font-size: 13px; color: #5d5d65; }
`;

function renderPage({ host, zone, signals, total, warming }) {
  const place = zone ? ZONES[zone] : null;

  // Three cases, and the unmapped one shows the raw host rather than inventing
  // a location for a name the map has never seen.
  let headline;
  if (zone && place) {
    headline =
      `<p class="zone">${escapeHTML(zone)}</p>` +
      `<p class="place">${escapeHTML(place)}</p>`;
  } else if (zone) {
    headline =
      `<p class="zone">${escapeHTML(zone)}</p>` +
      `<p class="place">zone not in this build's map</p>`;
  } else {
    headline = `<p class="zone unmapped">${escapeHTML(host || "unknown host")}</p>`;
  }

  let list;
  if (warming) {
    list = `<p class="empty">No signals yet — api warming.</p>`;
  } else if (signals.length === 0) {
    list = `<p class="empty">No signals yet. Light the first one.</p>`;
  } else {
    list =
      "<ul>" +
      signals
        .map(
          (signal) =>
            `<li><span class="body">${escapeHTML(signal.body)}</span>` +
            `<span class="when">${escapeHTML(relativeTime(signal.created_at))}</span></li>`,
        )
        .join("") +
      "</ul>";
  }

  const count = warming ? "—" : String(total);
  const noun = !warming && total === 1 ? "signal" : "signals";

  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>beacon</title>
<style>${STYLE}</style>
</head>
<body>
  <main class="page">
    <div class="wordmark"><span class="dot"></span>beacon</div>

    <p class="serving">Serving from</p>
    ${headline}
    <p class="host">${escapeHTML(host || "unknown host")}</p>

    <hr class="rule">

    <form method="post" action="/signal">
      <input type="text" name="body" maxlength="200" required autocomplete="off"
             placeholder="Say something worth carrying across a border">
      <button type="submit">Light a signal</button>
    </form>

    ${list}

    <footer>one glenr.yaml · data in managed Postgres · ${escapeHTML(count)} ${noun}</footer>
  </main>
</body>
</html>
`;
}

// parseForm reads a urlencoded body without pulling in a parser.
function parseForm(raw) {
  const params = new URLSearchParams(raw);
  return params.get("body") || "";
}

function readBody(request, limit = 64 * 1024) {
  return new Promise((resolve) => {
    const chunks = [];
    let size = 0;
    request.on("data", (chunk) => {
      size += chunk.length;
      if (size > limit) {
        request.destroy();
        resolve("");
        return;
      }
      chunks.push(chunk);
    });
    request.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    request.on("error", () => resolve(""));
  });
}

const server = http.createServer(async (request, response) => {
  const url = new URL(request.url, "http://placeholder");

  if (url.pathname === "/healthz") {
    response.writeHead(200, { "content-type": "text/plain; charset=utf-8" });
    response.end("ok");
    return;
  }

  // PRG: the post never renders, so a refresh cannot repost.
  if (request.method === "POST" && url.pathname === "/signal") {
    const body = parseForm(await readBody(request)).trim();
    if (body) await callAPI("POST", "/signals", { body: body.slice(0, 200) });
    response.writeHead(303, { location: "/" });
    response.end();
    return;
  }

  if (url.pathname !== "/") {
    response.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
    response.end("not found");
    return;
  }

  const host = request.headers.host || "";
  const listed = await callAPI("GET", "/signals");

  // ☠️ A COLD api MUST NOT PRODUCE A 500. The database may still be
  // provisioning when the first visitor arrives; the page says so and stays
  // beautiful rather than showing a stack trace to a room.
  const warming = !listed.ok || !Array.isArray(listed.body && listed.body.signals);
  const signals = warming ? [] : listed.body.signals;
  const total = warming ? 0 : Number(listed.body.total ?? signals.length);

  const html = renderPage({ host, zone: readZone(host), signals, total, warming });
  response.writeHead(200, {
    "content-type": "text/html; charset=utf-8",
    "cache-control": "no-store",
  });
  response.end(html);
});

server.listen(PORT, "0.0.0.0", () => {
  process.stdout.write(`beacon web listening on ${PORT}, api at ${API_HOST}:${API_PORT}\n`);
});

// Terminate promptly on a rolling update rather than waiting for a SIGKILL.
for (const signal of ["SIGTERM", "SIGINT"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
