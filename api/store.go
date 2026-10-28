package main

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type store interface {
	add(ctx context.Context, p ping) error
	// list returns up to maxPings pings, oldest first.
	list(ctx context.Context) ([]ping, error)
	name() string
}

// databaseURL finds the PostgreSQL URL the platform injected. A `connects:`
// to a database injects <NAME>_URL (and six more names), and the name is the
// one the database was given, so the URL is found by its scheme: DATABASE_URL
// first, then every other variable ending in _URL, in name order.
func databaseURL(environ []string) (string, bool) {
	values := map[string]string{}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if ok && strings.HasSuffix(name, "_URL") && isPostgresURL(value) {
			values[name] = value
		}
	}
	if v, ok := values["DATABASE_URL"]; ok {
		return v, true
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", false
	}
	return values[names[0]], true
}

func isPostgresURL(v string) bool {
	return strings.HasPrefix(v, "postgres://") || strings.HasPrefix(v, "postgresql://")
}

// ── memory ──────────────────────────────────────────────────────────────────

type memoryStore struct {
	mu    sync.Mutex
	pings []ping
}

func newMemoryStore() *memoryStore { return &memoryStore{} }

func (m *memoryStore) name() string { return "memory" }

func (m *memoryStore) add(_ context.Context, p ping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pings = append(m.pings, p)
	if len(m.pings) > maxPings {
		m.pings = m.pings[len(m.pings)-maxPings:]
	}
	return nil
}

func (m *memoryStore) list(_ context.Context) ([]ping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ping(nil), m.pings...), nil
}

// ── PostgreSQL ──────────────────────────────────────────────────────────────

// maxBackoff caps the wait between attempts to reach a database that is still
// being provisioned.
//
// The process listens before it has a database and retries without end. A
// managed database is created beside the service, and for a while its address
// resolves and nothing answers. A process that exits on that failure enters
// CrashLoopBackOff; one that serves only after connecting is killed by its
// liveness probe at about 60 s. Instead the connect runs in the background and
// requests get a 503 until it succeeds (the same shape as pulse's beacon/api,
// at commit 16d545a).
const maxBackoff = 5 * time.Second

const schema = `
CREATE TABLE IF NOT EXISTS pings (
	id      bigserial   PRIMARY KEY,
	sender  text        NOT NULL,
	at      timestamptz NOT NULL,
	note    text        NOT NULL DEFAULT ''
)`

var errConnecting = errors.New("the database is not connected yet")

type postgresStore struct {
	pool atomic.Pointer[pgxpool.Pool]
}

func newPostgresStore(ctx context.Context, url string) *postgresStore {
	s := &postgresStore{}
	go s.connect(ctx, url)
	return s
}

func (s *postgresStore) name() string { return "postgres" }

// connect runs until it has a pool and the table exists. The URL is handed to
// pgxpool as delivered; it carries ?sslmode=require already.
func (s *postgresStore) connect(ctx context.Context, url string) {
	wait := 250 * time.Millisecond
	for ctx.Err() == nil {
		pool, err := pgxpool.New(ctx, url)
		if err == nil {
			_, err = pool.Exec(ctx, schema)
			if err == nil {
				s.pool.Store(pool)
				log.Printf("pulse/api: connected to PostgreSQL")
				return
			}
			pool.Close()
		}
		log.Printf("pulse/api: waiting for PostgreSQL: %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait *= 2; wait > maxBackoff {
			wait = maxBackoff
		}
	}
}

func (s *postgresStore) add(ctx context.Context, p ping) error {
	pool := s.pool.Load()
	if pool == nil {
		return errConnecting
	}
	_, err := pool.Exec(ctx, `INSERT INTO pings (sender, at, note) VALUES ($1, $2, $3)`, p.From, p.At, p.Note)
	return err
}

func (s *postgresStore) list(ctx context.Context) ([]ping, error) {
	pool := s.pool.Load()
	if pool == nil {
		return nil, errConnecting
	}
	rows, err := pool.Query(ctx,
		`SELECT sender, at, note FROM (SELECT id, sender, at, note FROM pings ORDER BY id DESC LIMIT $1) latest ORDER BY id`,
		maxPings)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ping{}
	for rows.Next() {
		var p ping
		if err := rows.Scan(&p.From, &p.At, &p.Note); err != nil {
			return nil, err
		}
		p.At = p.At.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}
