package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
)

//go:embed web/index.html
var indexHTML []byte

// Server exposes the web console: a single page plus a small JSON API to view
// status and replace the key pool at runtime (no file edits, no restart).
type Server struct {
	cfg     *Config
	store   *Store
	rotator *Rotator
	trigger chan<- struct{}
}

func NewServer(cfg *Config, store *Store, rotator *Rotator, trigger chan<- struct{}) *Server {
	return &Server{cfg: cfg, store: store, rotator: rotator, trigger: trigger}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/keys", s.handleKeys)
	mux.HandleFunc("/api/rotate-now", s.handleRotateNow)
	return s.withAuth(mux)
}

// withAuth applies HTTP Basic Auth when a password is configured. The whole console
// manages API keys, so running it without a password is allowed only with a warning.
func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.cfg.WebPassword == "" {
		log.Printf("WARN WEB_PASSWORD is not set — the web console is UNPROTECTED; set it before exposing this service")
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.WebUsername)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.WebPassword)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="newapi-key-rotator"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rotator.Status())
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Keys string `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "invalid JSON body"})
		return
	}
	count, err := s.store.SetKeys(body.Keys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	log.Printf("INFO key pool replaced via console: %d key(s) loaded, progress reset", count)
	s.fire() // apply immediately if the channel is currently auto-disabled
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "count": count})
}

func (s *Server) handleRotateNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.fire()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// fire asks the rotation loop to run a tick now, without blocking if one is pending.
func (s *Server) fire() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
