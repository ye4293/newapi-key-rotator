package main

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
)

// Account 表示已验证的请求方。
type Account struct {
	IsAdmin  bool
	Label    string
	Channels []ChannelRef // 供货商可访问的渠道；IsAdmin=true 时忽略
}

type accountContextKey struct{}

// getAccount 从请求 context 取出 Account。调用前须经过 withAuth 中间件。
func getAccount(r *http.Request) Account {
	acc, _ := r.Context().Value(accountContextKey{}).(Account)
	return acc
}

// resolveAccount 解析 Basic Auth 凭据并返回对应账户。
// 先检查管理员密码，再遍历供货商账户（仅比对密码）。
func (s *Server) resolveAccount(r *http.Request) (Account, bool) {
	_, pass, ok := r.BasicAuth()
	if !ok {
		return Account{}, false
	}
	if s.cfg.WebPassword != "" {
		if subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.WebPassword)) == 1 {
			return Account{IsAdmin: true, Label: s.cfg.WebUsername}, true
		}
	}
	for _, acc := range s.cfg.Accounts {
		if subtle.ConstantTimeCompare([]byte(pass), []byte(acc.Password)) == 1 {
			return Account{IsAdmin: false, Label: acc.Label, Channels: acc.Channels}, true
		}
	}
	return Account{}, false
}

// canAccess 报告 acc 是否有权访问 inst。
func (s *Server) canAccess(acc Account, inst *instance) bool {
	if acc.IsAdmin {
		return true
	}
	for _, ref := range acc.Channels {
		if ref.InstIdx == inst.instIdx && ref.ChannelID == inst.channelID {
			return true
		}
	}
	return false
}

// withAuth 是全局 Basic Auth 中间件，将解析后的 Account 注入 context。
func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.cfg.WebPassword == "" {
		log.Printf("WARN WEB_PASSWORD is not set — the web console is UNPROTECTED; set it before exposing this service")
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), accountContextKey{}, Account{IsAdmin: true})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc, ok := s.resolveAccount(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), accountContextKey{}, acc)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin 若账户不是管理员则写 403 并返回 false。
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if getAccount(r).IsAdmin {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "需要管理员权限"})
	return false
}

// requireChannelAccess 若账户无权访问 inst 则写 403 并返回 false。
func (s *Server) requireChannelAccess(w http.ResponseWriter, r *http.Request, inst *instance) bool {
	if s.canAccess(getAccount(r), inst) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "无权访问此渠道"})
	return false
}
