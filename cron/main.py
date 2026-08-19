"""pulse/cron — once every five minutes, say how many pings the api holds and exit.

A cron run is a short process: read, print, exit 0. API_URL as above.
"""
import json, os, sys, urllib.request
from collections import Counter

# On Glenr, `connects: [api]` injects API_HOST/API_PORT; API_URL stays the
# compose/local override.
API_URL = os.environ.get("API_URL") or (
    f"http://{os.environ['API_HOST']}:{os.environ.get('API_PORT', '80')}"
    if os.environ.get("API_HOST")
    else "http://localhost:3000"
)
try:
    with urllib.request.urlopen(f"{API_URL}/pings", timeout=5) as r:
        data = json.load(r)
except Exception as e:  # noqa: BLE001
    print(f"cannot reach api at {API_URL}: {e}", file=sys.stderr)
    sys.exit(1)

by = Counter(p["from"] for p in data.get("pings", []))
print(f"{data.get('count', 0)} ping(s) held by the api")
for who, n in by.most_common():
    print(f"  {n:4d}  {who}")
