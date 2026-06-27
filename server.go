package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
)

//go:embed web/index.html
var indexHTML []byte

type Server struct {
	cfg       *Config
	instances []*instance
}

func NewServer(cfg *Config, instances []*instance) *Server {
	return &Server{cfg: cfg, instances: instances}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/instances", s.handleInstances)
	mux.HandleFunc("/api/instance/{idx}/status", s.handleInstanceStatus)
	mux.HandleFunc("/api/instance/{idx}/keys", s.handleInstanceKeys)
	mux.HandleFunc("/api/instance/{idx}/keys/append", s.handleInstanceKeysAppend)
	mux.HandleFunc("/api/instance/{idx}/rotate-now", s.handleInstanceRotateNow)
	mux.HandleFunc("/api/instance/{idx}/channel-id", s.handleInstanceChannelID)
	mux.HandleFunc("/api/instance/{idx}/pause", s.handleInstancePause)
	mux.HandleFunc("/api/instance/{idx}/resume", s.handleInstanceResume)
	mux.HandleFunc("/api/instance/{idx}/delete", s.handleInstanceDelete)
	// Legacy routes — delegate to instance 0 for backward compatibility.
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.instances[0].rotator.Status())
	})
	mux.HandleFunc("/api/keys", func(w http.ResponseWriter, r *http.Request) {
		s.keysHandler(w, r, s.instances[0])
	})
	mux.HandleFunc("/api/keys/append", func(w http.ResponseWriter, r *http.Request) {
		s.keysAppendHandler(w, r, s.instances[0])
	})
	mux.HandleFunc("/api/rotate-now", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fireInstance(s.instances[0].trigger)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	})
	return s.withAuth(mux)
}

func (s *Server) getInstance(r *http.Request) (*instance, bool) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil || idx < 0 || idx >= len(s.instances) {
		return nil, false
	}
	return s.instances[idx], true
}

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

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	type instanceInfo struct {
		Index     int    `json:"index"`
		BaseURL   string `json:"base_url"`
		ChannelID int    `json:"channel_id"`
	}
	infos := make([]instanceInfo, len(s.instances))
	for i, inst := range s.instances {
		infos[i] = instanceInfo{Index: i, BaseURL: inst.cfg.BaseURL, ChannelID: inst.cfg.ChannelID}
	}
	writeJSON(w, http.StatusOK, infos)
}

func (s *Server) handleInstanceStatus(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, inst.rotator.Status())
}

func (s *Server) handleInstanceKeys(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.keysHandler(w, r, inst)
}

func (s *Server) handleInstanceKeysAppend(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.keysAppendHandler(w, r, inst)
}

func (s *Server) handleInstanceDelete(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inst.rotator.Pause()
	if err := inst.store.SetDeleted(true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	log.Printf("INFO channel #%d marked as deleted (will be skipped on next restart)", inst.cfg.ChannelID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "实例已标记删除，重启后生效"})
}

func (s *Server) handleInstancePause(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inst.rotator.Pause()
	log.Printf("INFO channel #%d monitoring paused", inst.cfg.ChannelID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "paused": true})
}

func (s *Server) handleInstanceResume(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inst.rotator.Resume()
	fireInstance(inst.trigger)
	log.Printf("INFO channel #%d monitoring resumed", inst.cfg.ChannelID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "paused": false})
}

func (s *Server) handleInstanceChannelID(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		effective := inst.store.ChannelID(inst.cfg.ChannelID)
		writeJSON(w, http.StatusOK, map[string]any{
			"channel_id":         effective,
			"default_channel_id": inst.cfg.ChannelID,
			"is_custom":          effective != inst.cfg.ChannelID,
		})
	case http.MethodPost:
		var body struct {
			ChannelID int `json:"channel_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "invalid JSON"})
			return
		}
		if body.ChannelID < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "channel_id must be >= 0"})
			return
		}
		if err := inst.store.SetChannelOverride(body.ChannelID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		log.Printf("INFO channel override set to %d for instance (default %d)", body.ChannelID, inst.cfg.ChannelID)
		fireInstance(inst.trigger)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "channel_id": inst.store.ChannelID(inst.cfg.ChannelID)})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleInstanceRotateNow(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fireInstance(inst.trigger)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) keysHandler(w http.ResponseWriter, r *http.Request, inst *instance) {
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
	count, err := inst.store.SetKeys(body.Keys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	log.Printf("INFO channel #%d key pool replaced: %d key(s), progress reset", inst.cfg.ChannelID, count)
	fireInstance(inst.trigger)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "count": count})
}

func (s *Server) keysAppendHandler(w http.ResponseWriter, r *http.Request, inst *instance) {
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
	added, err := inst.store.AppendKeys(body.Keys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	log.Printf("INFO channel #%d key pool appended: %d new key(s) added", inst.cfg.ChannelID, added)
	if added > 0 {
		fireInstance(inst.trigger)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "added": added})
}

func fireInstance(trigger chan<- struct{}) {
	select {
	case trigger <- struct{}{}:
	default:
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
