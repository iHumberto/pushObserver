package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsolate(t *testing.T) {
	// Test basic Go 1.22+ mux routing with method patterns.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/hooks", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Handler called! Method=%s Path=%s", r.Method, r.URL.Path)
		http.Redirect(w, r, "/hooks/test?success=ok", http.StatusSeeOther)
	})

	body := "id=test&repo_url=x&repo_dir=/tmp"
	req := httptest.NewRequest("POST", "/api/hooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	t.Logf("Status: %d, Location: %s", rec.Code, rec.Header().Get("Location"))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("Expected 303, got %d", rec.Code)
	}
}

func TestIsolate_WithServer(t *testing.T) {
	s := newTestServer(t)

	// Call UI CreateHook directly (not through routes — UI method still exists).
	body := "id=testhook&repo_url=x&repo_dir=/tmp"
	req := httptest.NewRequest("POST", "/api/hooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	s.ui.CreateHook(rec, req)

	t.Logf("Status: %d, Location: %s, Body: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	// UI CreateHook returns 303 redirect on success.
	if rec.Code != http.StatusSeeOther {
		t.Errorf("UI CreateHook: expected 303, got %d", rec.Code)
	}
}
