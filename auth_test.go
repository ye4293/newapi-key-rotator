package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func makeTestServer(adminPw string, accounts []*AccountConfig) *Server {
	cfg := &Config{
		WebUsername: "admin",
		WebPassword: adminPw,
		Accounts:    accounts,
	}
	return &Server{cfg: cfg, instances: nil}
}

func TestCanAccess_admin(t *testing.T) {
	s := makeTestServer("secret", nil)
	inst := &instance{instIdx: 0, channelID: 42}
	acc := Account{IsAdmin: true}
	if !s.canAccess(acc, inst) {
		t.Error("admin should access any instance")
	}
}

func TestCanAccess_supplier_allowed(t *testing.T) {
	s := makeTestServer("secret", nil)
	inst := &instance{instIdx: 0, channelID: 42}
	acc := Account{IsAdmin: false, Channels: []ChannelRef{{InstIdx: 0, ChannelID: 42}}}
	if !s.canAccess(acc, inst) {
		t.Error("supplier should access authorized channel")
	}
}

func TestCanAccess_supplier_denied(t *testing.T) {
	s := makeTestServer("secret", nil)
	inst := &instance{instIdx: 0, channelID: 99}
	acc := Account{IsAdmin: false, Channels: []ChannelRef{{InstIdx: 0, ChannelID: 42}}}
	if s.canAccess(acc, inst) {
		t.Error("supplier should not access unauthorized channel")
	}
}

func TestResolveAccount_admin(t *testing.T) {
	s := makeTestServer("adminpw", nil)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("admin", "adminpw")
	acc, ok := s.resolveAccount(r)
	if !ok {
		t.Fatal("should resolve admin")
	}
	if !acc.IsAdmin {
		t.Error("should be admin")
	}
}

func TestResolveAccount_supplier(t *testing.T) {
	accounts := []*AccountConfig{
		{Password: "pw-a", Label: "供货商A", Channels: []ChannelRef{{InstIdx: 0, ChannelID: 42}}},
	}
	s := makeTestServer("adminpw", accounts)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("供货商A", "pw-a")
	acc, ok := s.resolveAccount(r)
	if !ok {
		t.Fatal("should resolve supplier")
	}
	if acc.IsAdmin {
		t.Error("should not be admin")
	}
	if acc.Label != "供货商A" {
		t.Errorf("want label=供货商A, got %q", acc.Label)
	}
}

func TestResolveAccount_invalid(t *testing.T) {
	s := makeTestServer("adminpw", nil)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("admin", "wrongpw")
	_, ok := s.resolveAccount(r)
	if ok {
		t.Error("should not resolve with wrong password")
	}
}
