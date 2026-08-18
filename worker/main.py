"""pulse/worker — proves it is alive by telling the api so, every 30 seconds.

No port, no HTTP server. API_URL is the api service's address; on Glenr it is
injected by a `type: service` connection.
"""
import json, os, socket, sys, time, urllib.request

API_URL = os.environ.get("API_URL", "http://localhost:3000")
EVERY = int(os.environ.get("PING_EVERY_SECONDS", "30"))
me = socket.gethostname()

print(f"pulse/worker {me} -> {API_URL} every {EVERY}s", flush=True)
n = 0
while True:
    n += 1
    body = json.dumps({"from": f"worker/{me}", "note": f"beat {n}"}).encode()
    req = urllib.request.Request(f"{API_URL}/pings", data=body,
                                 headers={"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=5) as r:
            print(f"beat {n}: api said {r.status}", flush=True)
    except Exception as e:  # noqa: BLE001 — a worker logs and keeps going
        print(f"beat {n}: cannot reach api: {e}", file=sys.stderr, flush=True)
    time.sleep(EVERY)
