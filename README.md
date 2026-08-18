# pulse

A tiny four-app system: something people look at, something it reads from, something
that writes to it on a loop, and something that summarises it on a schedule.

| app | kind | language | talks to |
|---|---|---|---|
| `web/` | public web service, port 8080 | Node | `api` |
| `api/` | internal service, port **3000**, in-memory | Go (scratch image) | — |
| `worker/ (main.py)` | long-running worker, no port | Python | `api` every 30s |
| `cron/ (main.py)` | scheduled job, `*/5 * * * *` | Python | `api` |

Every consumer finds the api through **`API_URL`**. Locally, `docker compose up` sets it;
on a platform, however that platform wires one service to another sets it.

Each directory carries a `glenr.yaml` naming its kind (and, for `cron`, its schedule) so
the platform does not have to guess which of these is a web server and which is a loop.
