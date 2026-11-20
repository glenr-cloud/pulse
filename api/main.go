// pulse/api — the internal record of who is alive.
//
// Nothing here knows it runs on Glenr. It listens on PORT (default 3000),
// keeps pings, and answers JSON. The worker POSTs, the web reads, the cron
// summarises.
//
// Pings live in memory. When a PostgreSQL URL is in the environment they live
// in that database instead (see databaseURL in store.go); the handlers do not
// know which.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// maxPings is how many pings a read returns.
const maxPings = 200

type ping struct {
	From string    `json:"from"`
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
}

type server struct {
	store store
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("/exit-check", s.exitCheck)
	mux.HandleFunc("/pings", s.pings)
	// The public route the exit test compares (P-5) points at "/". Answer it
	// with a stable body; every other unmatched path stays a 404.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("pulse\n"))
	})
	return mux
}

// adminHandler is the second listener's mux. It has no public route: the
// worker reaches it on the Service port named `admin` (design/145 §4.2).
func (s *server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	return mux
}

func (s *server) pings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	switch r.Method {
	case http.MethodPost:
		var p ping
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.From == "" {
			http.Error(w, `{"error":"a ping needs {from, note?}"}`, http.StatusBadRequest)
			return
		}
		p.At = time.Now().UTC()
		if err := s.store.add(ctx, p); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(p)
	default:
		out, err := s.store.list(ctx)
		if err != nil {
			storeError(w, err)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"count": len(out), "pings": out, "store": s.store.name()})
	}
}

// storeError answers 503 for a database that is not reachable yet or not
// answering. The worker retries every 30 s, so a 503 costs one ping.
func storeError(w http.ResponseWriter, err error) {
	log.Printf("store: %v", err)
	http.Error(w, `{"error":"the database is not available"}`, http.StatusServiceUnavailable)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	var st store = newMemoryStore()
	if url, ok := databaseURL(os.Environ()); ok {
		log.Printf("pulse/api: storing pings in PostgreSQL")
		st = newPostgresStore(context.Background(), url)
	} else {
		log.Printf("pulse/api: no database variable; storing pings in memory")
	}
	srv := &server{store: st}
	// Two listeners: the public one (PORT) and the `admin` port the worker
	// reaches (design/145 §4.2). Both must be up for the exit test, so the
	// admin listener runs on its own goroutine and a bind failure stops the
	// process rather than leaving a half-up app.
	adminPort := os.Getenv("ADMIN_PORT")
	if adminPort == "" {
		adminPort = "3001"
	}
	go func() {
		log.Printf("pulse/api admin listening on :%s", adminPort)
		if err := http.ListenAndServe(":"+adminPort, srv.adminHandler()); err != nil {
			log.Fatalf("pulse/api admin listener: %v", err)
		}
	}()
	log.Printf("pulse/api listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, srv.handler()))
}
