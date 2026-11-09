package api

import (
	"bytes"
	"encoding/json"
	"lab-router/internal/service"
	"lab-router/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticationCSRFAndSecrets(t *testing.T) {
	dir := t.TempDir()
	s, e := store.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	s.Init("test-admin-strong-password")
	svc := &service.ConfigService{Store: s, Dir: dir}
	handler := New(svc).Router()
	req := httptest.NewRequest("GET", "http://localhost/api/wifi", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("anonymous configuration access")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(`{"password":"test-admin-strong-password"}`)))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	var login map[string]string
	json.Unmarshal(w.Body.Bytes(), &login)
	req = httptest.NewRequest("GET", "http://localhost/api/wifi", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	c, _ := s.Config()
	if bytes.Contains(w.Body.Bytes(), []byte(c.WiFi.Password)) {
		t.Fatal("WiFi secret exposed")
	}
	req = httptest.NewRequest("PUT", "http://localhost/api/wifi", strings.NewReader(w.Body.String()))
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("CSRF not enforced")
	}
	req = httptest.NewRequest("POST", "http://localhost/api/backups", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", login["csrf"])
	req.Header.Set("Origin", "https://attacker.invalid")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	req = httptest.NewRequest("POST", "http://localhost/api/backups", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", login["csrf"])
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
}
func TestRebindingRejected(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.Open(dir)
	defer s.DB.Close()
	h := New(&service.ConfigService{Store: s, Dir: dir}).Router()
	req := httptest.NewRequest("GET", "http://attacker.invalid/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("Host validation missing")
	}
}
