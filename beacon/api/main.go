// beacon api — the only thing that touches Postgres.
//
// It receives its database from the platform: `connects: [maindb]` injects
// MAINDB_URL (among seven names), and that URL already carries
// `?sslmode=require`. It is handed to pgxpool verbatim — composing a DSN from
// the parts is how a service ends up accidentally speaking cleartext.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// maxBackoff caps the wait between attempts to reach a database that is still
// being provisioned.
//
// ☠️ THE PROCESS LISTENS BEFORE IT HAS A DATABASE, AND IT NEVER GIVES UP.
// Both halves of that are about CrashLoopBackOff, which is forbidden here.
//
// A managed Postgres is created alongside this service and there is a window —
// sometimes minutes — where the address resolves and nothing answers. The
// obvious shape (dial in main, exit on failure) enters CrashLoopBackOff, whose
// backoff is exponential: by the time the database is ready the kubelet is
// waiting five minutes before trying again.
//
// The less obvious shape (dial in main, retry for 90s, THEN serve) is also a
// trap, because of how probes render. A `health:` path becomes a readiness
// probe in the derived document, and the zone renderer uses a readiness-only
// document's path for the LIVENESS probe too (pkg/render/render.go: `case
// container.ReadinessProbe != nil: livenessPath = ...`). Liveness starts at 30s
// and kills after three 10s failures — so a process that has not opened its
// port by ~60s is killed at ~60s, and a database that took 70s to provision
// produces a pod that restarts forever while the database sits there healthy.
//
// So: listen immediately, connect in the background, retry forever with a
// capped backoff, and report warming state in the body rather than by being
// unreachable. A blip after boot is then also survivable — the same loop
// reconnects instead of the process dying.
const maxBackoff = 5 * time.Second

const schema = `
CREATE TABLE IF NOT EXISTS signals (
	id         bigserial   PRIMARY KEY,
	body       text        NOT NULL CHECK (length(body) BETWEEN 1 AND 200),
	created_at timestamptz NOT NULL DEFAULT now()
)`

type signal struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// server holds the pool behind an atomic pointer because the connect loop
// installs it from another goroutine while handlers are already serving.
type server struct {
	pool atomic.Pointer[pgxpool.Pool]
}

// db returns the pool, or nil while the database is still being reached.
func (s *server) db() *pgxpool.Pool { return s.pool.Load() }

// warming is the one sentence every handler gives when there is no database
// yet. It is a 503 so the caller can tell "not ready" from "broken", and the
// web front end turns exactly this into "api warming" rather than an error.
func warmingResponse(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "the database is still being provisioned")
}

// writeJSON is the only way a response leaves this process, so no handler can
// accidentally emit an HTML error page into something expecting JSON.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// recoverer turns a panic into a 500 with a JSON body. A panic that escaped
// would close the connection mid-response and show a browser error page.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic serving %s %s: %v", r.Method, r.URL.Path, recovered)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) listSignals(w http.ResponseWriter, r *http.Request) {
	pool := s.db()
	if pool == nil {
		warmingResponse(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx,
		`SELECT body, created_at FROM signals ORDER BY id DESC LIMIT 50`)
	if err != nil {
		log.Printf("list signals: %v", err)
		writeError(w, http.StatusServiceUnavailable, "the database is not answering")
		return
	}
	defer rows.Close()

	signals := make([]signal, 0, 50)
	for rows.Next() {
		var s signal
		if err := rows.Scan(&s.Body, &s.CreatedAt); err != nil {
			log.Printf("scan signal: %v", err)
			writeError(w, http.StatusInternalServerError, "could not read the signals")
			return
		}
		signals = append(signals, s)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate signals: %v", err)
		writeError(w, http.StatusServiceUnavailable, "the database is not answering")
		return
	}

	// The footer's count is the WHOLE table, not the page — "50 signals"
	// forever would be a quiet lie once the fifty-first arrives.
	var total int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM signals`).Scan(&total); err != nil {
		log.Printf("count signals: %v", err)
		total = int64(len(signals))
	}

	writeJSON(w, http.StatusOK, map[string]any{"signals": signals, "total": total})
}

func (s *server) createSignal(w http.ResponseWriter, r *http.Request) {
	pool := s.db()
	if pool == nil {
		warmingResponse(w)
		return
	}

	var input struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "the request body must be JSON shaped {\"body\": \"...\"}")
		return
	}

	body := strings.TrimSpace(input.Body)
	if body == "" {
		writeError(w, http.StatusBadRequest, "a signal needs something to say")
		return
	}
	// Bounded here as well as by the CHECK constraint, so an over-long body is
	// a sentence rather than a constraint violation.
	if len([]rune(body)) > 200 {
		writeError(w, http.StatusBadRequest, "a signal is at most 200 characters")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var created signal
	err := pool.QueryRow(ctx,
		`INSERT INTO signals (body) VALUES ($1) RETURNING body, created_at`, body,
	).Scan(&created.Body, &created.CreatedAt)
	if err != nil {
		log.Printf("insert signal: %v", err)
		writeError(w, http.StatusServiceUnavailable, "the signal could not be stored")
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	pool := s.db()
	if pool == nil {
		warmingResponse(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var count int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM signals`).Scan(&count); err != nil {
		log.Printf("healthz: %v", err)
		writeError(w, http.StatusServiceUnavailable, "the database is not answering")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signals": count})
}

// connectForever dials the database and applies the schema, retrying until it
// succeeds. It installs the pool on the server the moment it is usable, which
// is what flips every handler from "warming" to serving.
func connectForever(ctx context.Context, s *server, url string) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		// Not retryable: a malformed URL will not become well-formed. Say so
		// once, loudly, and leave the process listening so the sentence is
		// reachable rather than hidden behind a restart loop.
		log.Printf("MAINDB_URL is not a usable connection string: %v", err)
		return
	}
	config.MaxConns = 8
	config.MaxConnLifetime = 30 * time.Minute

	delay := 500 * time.Millisecond
	for attempt := 1; ; attempt++ {
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				// ☠️ THE SCHEMA IS APPLIED BEFORE THE POOL IS PUBLISHED. A pool
				// installed first would let a request arrive between the ping
				// and the CREATE TABLE and fail on a missing relation — a race
				// that only ever shows up on the first deploy, which is the one
				// being filmed.
				if _, err = pool.Exec(ctx, schema); err == nil {
					s.pool.Store(pool)
					log.Printf("database ready after %d attempt(s)", attempt)
					return
				}
			}
			pool.Close()
		}

		log.Printf("database not ready (attempt %d): %v — retrying in %s", attempt, err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < maxBackoff {
			delay *= 2
			if delay > maxBackoff {
				delay = maxBackoff
			}
		}
	}
}

func main() {
	log.SetFlags(0)

	url := os.Getenv("MAINDB_URL")
	if url == "" {
		// Named exactly, because the fix is a `connects: [maindb]` on this
		// resource's row and nothing else.
		log.Fatal("MAINDB_URL is not set — this service needs `connects: [maindb]` on its glenr.yaml row")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	ctx := context.Background()
	s := &server{}
	go connectForever(ctx, s, url)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /signals", s.listSignals)
	mux.HandleFunc("POST /signals", s.createSignal)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint — this api serves /signals and /healthz")
	})

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           recoverer(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("beacon api listening on %s", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server stopped: %v", err)
	}
}
