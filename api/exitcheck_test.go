package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestExitCheckHashesEveryInput drives buildExitCheck with a crafted
// environment: one plain file, one secret env var, one values env var, a
// scratch directory and a seeded-table name. The memory store has no database,
// so the db section reports why it is uncounted rather than failing.
func TestExitCheckHashesEveryInput(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(plain, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(dir, "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{
		envFiles + "=" + plain,
		envSecretNames + "=DB_PASSWORD",
		envValueNames + "=GREETING",
		envScratch + "=" + scratch,
		envTable + "=seed",
		"DB_PASSWORD=hunter2",
		"GREETING=hi",
	}
	got := buildExitCheck(context.Background(), env, newMemoryStore())

	if want := hashString("hello"); got.Files[plain] != want {
		t.Errorf("files[%s] = %q, want %q", plain, got.Files[plain], want)
	}
	if want := hashString("hunter2"); got.Secret["DB_PASSWORD"] != want {
		t.Errorf("secret[DB_PASSWORD] = %q, want %q", got.Secret["DB_PASSWORD"], want)
	}
	if want := hashString("hi"); got.Value["GREETING"] != want {
		t.Errorf("value[GREETING] = %q, want %q", got.Value["GREETING"], want)
	}
	if got.Scratch != "ok" {
		t.Errorf("scratch = %q, want ok", got.Scratch)
	}
	if got.DB.Table != "seed" || got.DB.Rows != nil || got.DB.Note == "" {
		t.Errorf("db = %+v, want table=seed, no rows, a note", got.DB)
	}
}

// TestExitCheckBodyIsStable is P-5's premise: two runs over the same inputs
// produce the same bytes.
func TestExitCheckBodyIsStable(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{envFiles + "=" + a, envScratch + "=" + dir}
	ctx := context.Background()
	b1, err := json.Marshal(buildExitCheck(ctx, env, newMemoryStore()))
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(buildExitCheck(ctx, env, newMemoryStore()))
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("body not stable:\n%s\n%s", b1, b2)
	}
}

// A missing file is reported in the body, not as a failed request, so P-5
// shows which file each cluster lacks.
func TestExitCheckMissingFileIsReported(t *testing.T) {
	got := buildExitCheck(context.Background(),
		[]string{envFiles + "=/no/such/file", envScratch + "=" + t.TempDir()}, newMemoryStore())
	v := got.Files["/no/such/file"]
	if len(v) < 6 || v[:6] != "error:" {
		t.Fatalf("files[/no/such/file] = %q, want an error string", v)
	}
}

// Concurrent checks on one pod must not corrupt each other's scratch file;
// the body has to stay a function of the inputs for P-5.
func TestScratchCheckConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() { errs <- scratchCheck(dir) }()
	}
	for i := 0; i < n; i++ {
		if got := <-errs; got != "ok" {
			t.Errorf("concurrent scratchCheck = %q, want ok", got)
		}
	}
}

// The table name is trimmed, so a value carrying a trailing newline from a
// YAML/values source is still accepted instead of silently disabling the
// row count on both clusters.
func TestDBTableTrimmed(t *testing.T) {
	got := dbCheck(context.Background(), "  seed\n", newMemoryStore())
	if got.Table != "seed" {
		t.Errorf("table = %q, want seed", got.Table)
	}
	if got.Note != errNoDatabase.Error() {
		t.Errorf("note = %q, want the no-database note", got.Note)
	}
}

func TestValidTable(t *testing.T) {
	ok := []string{"pings", "public.pings", "_x", "a1"}
	bad := []string{"", "Pings", "1abc", "a.b.c", "pings; drop table x", `pings"`}
	for _, s := range ok {
		if !validTable(s) {
			t.Errorf("validTable(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validTable(s) {
			t.Errorf("validTable(%q) = true, want false", s)
		}
	}
}

// The HTTP handler serves the body as JSON.
func TestExitCheckHTTP(t *testing.T) {
	h := (&server{store: newMemoryStore()}).handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/exit-check", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	var body exitCheckBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, rec.Body)
	}
}

// GET / answers a stable body; any other unmatched path is a 404.
func TestIndex(t *testing.T) {
	h := (&server{store: newMemoryStore()}).handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "pulse\n" {
		t.Errorf("GET / = %d %q", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", rec.Code)
	}
}

// The admin listener answers health and does not serve the public endpoints.
func TestAdminListener(t *testing.T) {
	h := (&server{store: newMemoryStore()}).adminHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Errorf("GET /healthz = %d %q", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/exit-check", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("admin serves /exit-check: %d", rec.Code)
	}
}
