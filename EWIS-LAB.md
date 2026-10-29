# EWIS lab branches

Branches for the EWIS engineer certification lab (Paperclip GLE-132). `main` is the 4-app sample and is not changed.

| Branch | Difference from `main` |
|---|---|
| `ewis-lab` | `api/glenr.yaml`: `expose: public`, `port: 3000`. `worker/glenr.yaml`: `connects: [api]`. A repository build takes its connections from this file; the console form refuses a connection on a repository build. |
| `fault-b` | `ewis-lab` plus `health: /nope` in `api/glenr.yaml`: the readiness probe never passes, the rollout stops, the previous version keeps serving. |
| `fault-c` | `ewis-lab` plus `env: DATABASE_URL` in `api/glenr.yaml` set to an unreachable address: `/healthz` stays 200, `/pings` returns 503. |
