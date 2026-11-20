package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// The exit test reads /exit-check on the `web` app (design/145 §4.2). The
// endpoint proves that what a customer's own cluster needs actually arrived:
// a plain file, a secret file, an environment secret, a values variable, a
// scratch volume and a database connection. Every input is named by an
// environment variable that the gate cell's Score document sets, so this
// program carries no cell-specific paths.
//
// The body is compared byte for byte across two clusters (pass criterion P-5:
// GET /exit-check on lz-2 and on the target must return the same status and
// body sha256). So nothing in the body may vary run to run except the values
// being checked. encoding/json sorts map keys, so the maps below are stable.

const (
	// envFiles lists file paths to hash, comma-separated. A missing file adds
	// an "error: ..." entry rather than failing the request, so P-5 compares
	// what each cluster actually has.
	envFiles = "EXIT_CHECK_FILES"
	// envSecretNames lists environment variable names (the envFrom Secret) to
	// hash, comma-separated.
	envSecretNames = "EXIT_CHECK_SECRET_ENVS"
	// envValueNames lists environment variable names holding values variables.
	envValueNames = "EXIT_CHECK_VALUE_ENVS"
	// envScratch is a directory on the scratch volume the endpoint writes to
	// and reads back.
	envScratch = "EXIT_CHECK_SCRATCH"
	// envTable names the seeded table whose row count is printed.
	envTable = "EXIT_CHECK_DB_TABLE"
)

// hashPrefix marks a value this endpoint hashed.
const hashPrefix = "sha256:"

// exitCheckBody is the JSON the endpoint serves. Field order is fixed; the
// maps inside are sorted by json.Marshal.
type exitCheckBody struct {
	Files   map[string]string `json:"files,omitempty"`
	Secret  map[string]string `json:"secret,omitempty"`
	Value   map[string]string `json:"value,omitempty"`
	Scratch string            `json:"scratch"`
	DB      exitCheckDB       `json:"db"`
}

type exitCheckDB struct {
	Table string `json:"table,omitempty"`
	Rows  *int64 `json:"rows,omitempty"`
	Note  string `json:"note,omitempty"`
}

// exitCheckHandler serves GET /exit-check.
func (s *server) exitCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := buildExitCheck(ctx, os.Environ(), s.store)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}

// buildExitCheck runs each check named in environ and returns the body. It is
// pure of HTTP so the tests drive it directly.
func buildExitCheck(ctx context.Context, environ []string, st store) exitCheckBody {
	env := environMap(environ)
	out := exitCheckBody{
		Files:   map[string]string{},
		Secret:  map[string]string{},
		Value:   map[string]string{},
		Scratch: scratchCheck(env[envScratch]),
	}
	for _, path := range splitList(env[envFiles]) {
		out.Files[path] = hashFile(path)
	}
	for _, name := range splitList(env[envSecretNames]) {
		out.Secret[name] = hashValue(env, name)
	}
	for _, name := range splitList(env[envValueNames]) {
		out.Value[name] = hashValue(env, name)
	}
	out.DB = dbCheck(ctx, env[envTable], st)
	return out
}

func environMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if ok {
			m[name] = value
		}
	}
	return m
}

func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func hashValue(env map[string]string, name string) string {
	v, ok := env[name]
	if !ok {
		return "error: variable " + name + " is not set"
	}
	return hashString(v)
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hashPrefix + hex.EncodeToString(sum[:])
}

// hashFile hashes a file, or returns an "error: ..." string. The string, not a
// Go error, keeps the body shape uniform and comparable.
func hashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "error: " + err.Error()
	}
	return hashString(string(data))
}

// scratchCheck writes a file to the scratch volume and reads it back. The
// content is fixed so a healthy cluster reports simply "ok". The file name is
// unique per request (os.CreateTemp) so two concurrent GETs on one pod cannot
// truncate each other's file and make the body non-deterministic.
func scratchCheck(dir string) string {
	if dir == "" {
		return "error: " + envScratch + " is not set"
	}
	const content = "pulse exit-check scratch"
	f, err := os.CreateTemp(dir, "exit-check-*")
	if err != nil {
		return "error: create: " + err.Error()
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return "error: write: " + err.Error()
	}
	if err := f.Close(); err != nil {
		return "error: close: " + err.Error()
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return "error: read: " + err.Error()
	}
	if string(got) != content {
		return "error: read back " + string(got)
	}
	return "ok"
}

// tableName matches one PostgreSQL identifier part (lowercase, as the cell's
// Score documents name tables). A qualified name `schema.table` is two parts.
var tableName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// validTable reports whether table is `name` or `schema.name` with each part a
// simple lowercase identifier, so it can be quoted into SQL safely.
func validTable(table string) bool {
	parts := strings.Split(table, ".")
	if len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if !tableName.MatchString(p) {
			return false
		}
	}
	return true
}

func dbCheck(ctx context.Context, table string, st store) exitCheckDB {
	table = strings.TrimSpace(table)
	if table == "" {
		return exitCheckDB{Note: envTable + " is not set"}
	}
	if !validTable(table) {
		return exitCheckDB{Table: table, Note: "not a table name"}
	}
	rows, err := st.rowCount(ctx, table)
	if err != nil {
		return exitCheckDB{Table: table, Note: err.Error()}
	}
	return exitCheckDB{Table: table, Rows: &rows}
}
