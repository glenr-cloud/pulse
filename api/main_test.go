package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type listing struct {
	Count int    `json:"count"`
	Pings []ping `json:"pings"`
	Store string `json:"store"`
}

func do(t *testing.T, h http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/pings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func readAll(t *testing.T, h http.Handler) listing {
	t.Helper()
	rec := do(t, h, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /pings = %d: %s", rec.Code, rec.Body)
	}
	var l listing
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestMemoryRoundTrip(t *testing.T) {
	h := (&server{store: newMemoryStore()}).handler()
	if rec := do(t, h, http.MethodPost, `{"from":"worker","note":"hello"}`); rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body)
	}
	l := readAll(t, h)
	if l.Count != 1 || l.Pings[0].From != "worker" || l.Pings[0].Note != "hello" || l.Store != "memory" {
		t.Fatalf("got %+v", l)
	}
}

func TestPostNeedsFrom(t *testing.T) {
	h := (&server{store: newMemoryStore()}).handler()
	for _, body := range []string{`{}`, `{"note":"x"}`, `not json`} {
		if rec := do(t, h, http.MethodPost, body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %q = %d, want 400", body, rec.Code)
		}
	}
}

func TestMemoryKeepsTheLast200(t *testing.T) {
	h := (&server{store: newMemoryStore()}).handler()
	for i := 0; i < maxPings+5; i++ {
		do(t, h, http.MethodPost, fmt.Sprintf(`{"from":"w","note":"%d"}`, i))
	}
	l := readAll(t, h)
	if l.Count != maxPings || l.Pings[0].Note != "5" || l.Pings[maxPings-1].Note != fmt.Sprint(maxPings+4) {
		t.Fatalf("count %d, first %q, last %q", l.Count, l.Pings[0].Note, l.Pings[len(l.Pings)-1].Note)
	}
}

func TestDatabaseURL(t *testing.T) {
	cases := []struct {
		name    string
		environ []string
		want    string
		ok      bool
	}{
		{"none", []string{"PORT=3000", "PATH=/bin"}, "", false},
		{"named by the database", []string{"PULSEDB_URL=postgres://u:p@h:5432/d?sslmode=require"}, "postgres://u:p@h:5432/d?sslmode=require", true},
		{"postgresql scheme", []string{"DB_URL=postgresql://u@h/d"}, "postgresql://u@h/d", true},
		{"a service URL is not a database", []string{"API_URL=http://api:3000"}, "", false},
		{"DATABASE_URL wins", []string{"A_URL=postgres://a/a", "DATABASE_URL=postgres://d/d"}, "postgres://d/d", true},
		{"first by name", []string{"Z_URL=postgres://z/z", "B_URL=postgres://b/b"}, "postgres://b/b", true},
		{"the parts alone do not count", []string{"DB_HOST=h", "DB_PASSWORD=p"}, "", false},
	}
	for _, c := range cases {
		got, ok := databaseURL(c.environ)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// A database that is configured but not reachable answers 503, it does not
// fall back to memory: a ping accepted into memory would be lost on restart
// while the caller believes it was stored.
func TestUnreachableDatabaseAnswers503(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := (&server{store: newPostgresStore(ctx, "postgres://u:p@127.0.0.1:1/d?sslmode=disable&connect_timeout=1")}).handler()
	if rec := do(t, h, http.MethodPost, `{"from":"w"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("POST = %d, want 503", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET = %d, want 503", rec.Code)
	}
}

// TestPostgres runs against a real database: TEST_DATABASE_URL names an empty
// one. It skips without it.
func TestPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := newPostgresStore(ctx, url)
	waitConnected(t, first)
	if _, err := first.pool.Load().Exec(ctx, `TRUNCATE pings`); err != nil {
		t.Fatal(err)
	}
	h := (&server{store: first}).handler()
	for i := 0; i < maxPings+3; i++ {
		if rec := do(t, h, http.MethodPost, fmt.Sprintf(`{"from":"w","note":"%d"}`, i)); rec.Code != http.StatusCreated {
			t.Fatalf("POST %d = %d: %s", i, rec.Code, rec.Body)
		}
	}
	l := readAll(t, h)
	if l.Store != "postgres" || l.Count != maxPings || l.Pings[0].Note != "3" || l.Pings[maxPings-1].Note != fmt.Sprint(maxPings+2) {
		t.Fatalf("store %q, count %d, first %q, last %q", l.Store, l.Count, l.Pings[0].Note, l.Pings[len(l.Pings)-1].Note)
	}
	if l.Pings[0].At.IsZero() || time.Since(l.Pings[0].At) > time.Minute {
		t.Errorf("at = %v", l.Pings[0].At)
	}

	// The rows outlive the process: a second store on the same database
	// reads what the first wrote.
	second := newPostgresStore(ctx, url)
	waitConnected(t, second)
	l2 := readAll(t, (&server{store: second}).handler())
	if l2.Count != maxPings || l2.Pings[maxPings-1].Note != fmt.Sprint(maxPings+2) {
		t.Fatalf("after reconnect: count %d, last %q", l2.Count, l2.Pings[len(l2.Pings)-1].Note)
	}
}

func waitConnected(t *testing.T, s *postgresStore) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.pool.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("no connection within 10 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
