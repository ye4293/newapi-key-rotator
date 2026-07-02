package main

import (
	"bytes"
	_ "embed"
	"encoding/base64"
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
	mux.HandleFunc("/api/instance/{idx}/label", s.handleInstanceLabel)
	mux.HandleFunc("/api/me", s.handleMe)
	// Legacy routes — delegate to instance 0 for backward compatibility.
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if len(s.instances) == 0 {
			http.NotFound(w, r)
			return
		}
		inst := s.instances[0]
		if !s.requireChannelAccess(w, r, inst) {
			return
		}
		writeJSON(w, http.StatusOK, inst.rotator.Status())
	})
	mux.HandleFunc("/api/keys", func(w http.ResponseWriter, r *http.Request) {
		if len(s.instances) == 0 {
			http.NotFound(w, r)
			return
		}
		inst := s.instances[0]
		if !s.requireChannelAccess(w, r, inst) {
			return
		}
		s.keysHandler(w, r, inst)
	})
	mux.HandleFunc("/api/keys/append", func(w http.ResponseWriter, r *http.Request) {
		if len(s.instances) == 0 {
			http.NotFound(w, r)
			return
		}
		inst := s.instances[0]
		if !s.requireChannelAccess(w, r, inst) {
			return
		}
		s.keysAppendHandler(w, r, inst)
	})
	mux.HandleFunc("/api/rotate-now", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if len(s.instances) == 0 {
			http.NotFound(w, r)
			return
		}
		inst := s.instances[0]
		if !s.requireAdmin(w, r) {
			return
		}
		fireInstance(inst.trigger)
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

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	user, pass, _ := r.BasicAuth()
	token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	page := bytes.Replace(indexHTML, []byte("__AUTH_TOKEN__"), []byte(token), 1)
	_, _ = w.Write(page)
}

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	acc := getAccount(r)
	type instanceInfo struct {
		Index     int    `json:"index"`
		BaseURL   string `json:"base_url"`
		ChannelID int    `json:"channel_id"`
		InstIdx   int    `json:"inst_idx"`
		Platform  string `json:"platform"`
		Label     string `json:"label"`
		IsAdmin   bool   `json:"is_admin"`
	}
	infos := make([]instanceInfo, 0, len(s.instances))
	for i, inst := range s.instances {
		if inst.store.IsDeleted() {
			continue
		}
		if !s.canAccess(acc, inst) {
			continue
		}
		infos = append(infos, instanceInfo{
			Index:     i,
			BaseURL:   inst.cfg.BaseURL,
			ChannelID: inst.channelID,
			InstIdx:   inst.instIdx,
			Platform:  inst.cfg.Platform,
			Label:     inst.store.GetLabel(),
			IsAdmin:   acc.IsAdmin,
		})
	}
	writeJSON(w, http.StatusOK, infos)
}

func (s *Server) handleInstanceStatus(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.requireChannelAccess(w, r, inst) {
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
	if !s.requireChannelAccess(w, r, inst) {
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
	if !s.requireChannelAccess(w, r, inst) {
		return
	}
	s.keysAppendHandler(w, r, inst)
}

func (s *Server) handleInstanceLabel(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !s.requireChannelAccess(w, r, inst) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"label": inst.store.GetLabel()})
	case http.MethodPost:
		if !s.requireAdmin(w, r) {
			return
		}
		var body struct {
			Label string `json:"label"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "invalid JSON"})
			return
		}
		if err := inst.store.SetLabel(body.Label); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "label": body.Label})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleInstanceDelete(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.requireAdmin(w, r) {
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
	log.Printf("INFO channel #%d marked as deleted (will be skipped on next restart)", inst.channelID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "实例已标记删除，重启后生效"})
}

func (s *Server) handleInstancePause(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inst.rotator.Pause()
	log.Printf("INFO channel #%d monitoring paused", inst.channelID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "paused": true})
}

func (s *Server) handleInstanceResume(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.getInstance(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inst.rotator.Resume()
	fireInstance(inst.trigger)
	log.Printf("INFO channel #%d monitoring resumed", inst.channelID)
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
		if !s.requireChannelAccess(w, r, inst) {
			return
		}
		effective := inst.store.ChannelID(inst.channelID)
		writeJSON(w, http.StatusOK, map[string]any{
			"channel_id":         effective,
			"default_channel_id": inst.channelID,
			"is_custom":          effective != inst.channelID,
		})
	case http.MethodPost:
		if !s.requireAdmin(w, r) {
			return
		}
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
		log.Printf("INFO channel override set to %d for instance (default %d)", body.ChannelID, inst.channelID)
		fireInstance(inst.trigger)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "channel_id": inst.store.ChannelID(inst.channelID)})
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
	if !s.requireAdmin(w, r) {
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
	log.Printf("INFO channel #%d key pool replaced: %d key(s), progress reset", inst.channelID, count)
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
	log.Printf("INFO channel #%d key pool appended: %d new key(s) added", inst.channelID, added)
	if added > 0 {
		fireInstance(inst.trigger)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "added": added})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	acc := getAccount(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"is_admin": acc.IsAdmin,
		"label":    acc.Label,
	})
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
