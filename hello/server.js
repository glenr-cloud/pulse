// hello — one service for the enablement demo (branch demo-prep). Version 1.
const http = require("node:http");
const VERSION = "v1";
const PORT = process.env.PORT || 8080;
http.createServer((req, res) => {
  if (req.url === "/healthz") { res.writeHead(200); return res.end("ok\n"); }
  const db = Object.keys(process.env).some((k) => k.endsWith("_URL") && k.startsWith("MAINDB")) ? "connected" : "not connected";
  res.writeHead(200, { "Content-Type": "text/plain; charset=utf-8" });
  res.end(`hello ${VERSION} · database ${db}\n`);
}).listen(PORT);
