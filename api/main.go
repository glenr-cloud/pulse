// pulse/api — the internal record of who is alive.
//
// Nothing here knows it runs on Glenr. It listens on PORT (default 3000),
// keeps pings in memory, and answers JSON. The worker POSTs, the web reads,
// the cron summarises.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type ping struct {
	From string    `json:"from"`
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
}

var (
	mu    sync.Mutex
	pings []ping
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("/pings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			var p ping
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.From == "" {
				http.Error(w, `{"error":"a ping needs {from, note?}"}`, http.StatusBadRequest)
				return
			}
			p.At = time.Now().UTC()
			mu.Lock()
			pings = append(pings, p)
			if len(pings) > 200 {
				pings = pings[len(pings)-200:]
			}
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(p)
		default:
			mu.Lock()
			out := append([]ping(nil), pings...)
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"count": len(out), "pings": out})
		}
	})
	log.Printf("pulse/api listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
