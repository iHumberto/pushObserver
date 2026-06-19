package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsolate_Routes(t *testing.T) {
	s, _ := newTestServerWithFile(t)

	// POST /api/hooks now expects JSON body (REST API, not UI form).
	body := map[string]interface{}{
		"id":       "isolate-test",
		"repo_url": "git@github.com:test/repo.git",
		"repo_dir": "/tmp/test",
		"branch":   "main",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/hooks", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	t.Log("Sending POST /api/hooks...")
	s.routes().ServeHTTP(rec, req)

	t.Logf("Status: %d, Location: %s, Body: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())

	// REST API returns 201 Created on success.
	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON response, got Content-Type: %q", ct)
	}
}
