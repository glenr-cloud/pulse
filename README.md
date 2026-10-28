# pulse

A tiny four-app system: something people look at, something it reads from, something
that writes to it on a loop, and something that summarises it on a schedule.

| app | kind | language | talks to |
|---|---|---|---|
| `web/` | public web service, port 8080 | Node | `api` |
| `api/` | internal service, port **3000**; in memory, or in PostgreSQL when a database is connected | Go (scratch image) | PostgreSQL (optional) |
| `worker/ (main.py)` | long-running worker, no port | Python | `api` every 30s |
| `cron/ (main.py)` | scheduled job, `*/5 * * * *` | Python | `api` |

Every consumer finds the api through **`API_URL`**. Locally, `docker compose up` sets it;
on a platform, however that platform wires one service to another sets it.

Each directory carries a `glenr.yaml` naming its kind (and, for `cron`, its schedule) so
the platform does not have to guess which of these is a web server and which is a loop.

## Storage

`api/` keeps its pings in memory. Connect a PostgreSQL database to it and it stores them in a
`pings` table instead: it uses the first environment variable that ends in `_URL` and starts
with `postgres://` or `postgresql://` (`DATABASE_URL` first). On Glenr, connecting `api` to a
database named `pulsedb` injects `PULSEDB_URL` (and six more names); the URL already carries
`sslmode=require`. `GET /pings` names the store it read from in its `store` field.

Until the database answers, `api/` listens and returns 503 on `/pings`; it never falls back to
memory when a database is configured. `healthz` stays 200.

Test: `cd api && go test ./...`. With `TEST_DATABASE_URL` set to an empty database the test
also runs against PostgreSQL.
