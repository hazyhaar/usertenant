// CLAUDE:SUMMARY HTTP handler for pool administration: list shards, pool stats, update strategy.
package tenant

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// AdminHandler returns an http.Handler that exposes pool administration
// endpoints:
//
//	GET  /shards                       — list all shards
//	GET  /shards/{dossierID}           — get a single shard
//	GET  /pool/stats                   — pool statistics
//	POST /shards/{dossierID}/strategy  — update shard strategy
func AdminHandler(pool *Pool) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /pool/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, pool.Stats())
	})

	mux.HandleFunc("GET /shards", func(w http.ResponseWriter, r *http.Request) {
		pool.mu.RLock()
		shards := make([]shard, 0, len(pool.shardSnap))
		for _, s := range pool.shardSnap {
			shards = append(shards, s)
		}
		pool.mu.RUnlock()
		writeJSON(w, http.StatusOK, shards)
	})

	mux.HandleFunc("GET /shards/{dossierID}", func(w http.ResponseWriter, r *http.Request) {
		dossierID := r.PathValue("dossierID")

		pool.mu.RLock()
		s, ok := pool.shardSnap[dossierID]
		pool.mu.RUnlock()

		if !ok {
			http.Error(w, "shard not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, s)
	})

	mux.HandleFunc("POST /shards/{dossierID}/strategy", func(w http.ResponseWriter, r *http.Request) {
		dossierID := r.PathValue("dossierID")

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var req struct {
			Strategy string          `json:"strategy"`
			Endpoint string          `json:"endpoint"`
			Config   json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		req.Strategy = strings.TrimSpace(req.Strategy)
		if req.Strategy == "" {
			http.Error(w, "strategy is required", http.StatusBadRequest)
			return
		}

		if err := pool.SetStrategy(r.Context(), dossierID, req.Strategy, req.Endpoint, req.Config); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Trigger a reload so the change takes effect immediately.
		if err := pool.Reload(r.Context()); err != nil {
			http.Error(w, "reload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
