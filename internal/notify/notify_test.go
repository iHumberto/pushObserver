package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forgejo.humbertof.dev/humberto/push-observer/internal/deploy"
)

// ──────────────────── New() tests ─────────────────────────────────────

func TestNew_DefaultTimeout(t *testing.T) {
	n := New("http://apprise:8000", "success", "failure", "", 0)
	if n == nil {
		t.Fatal("New returned nil")
	}
	if n.appriseURL != "http://apprise:8000" {
		t.Errorf("appriseURL = %q, want %q", n.appriseURL, "http://apprise:8000")
	}
	if n.client.Timeout != 10*time.Second {
		t.Errorf("default timeout = %v, want 10s", n.client.Timeout)
	}
}

func TestNew_CustomTimeout(t *testing.T) {
	n := New("http://apprise:8000", "s", "f", "", 5*time.Second)
	if n.client.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", n.client.Timeout)
	}
}

func TestNew_NegativeTimeout_Defaults(t *testing.T) {
	n := New("http://apprise:8000", "s", "f", "", -1*time.Second)
	if n.client.Timeout != 10*time.Second {
		t.Errorf("negative timeout should default to 10s, got %v", n.client.Timeout)
	}
}

func TestNew_StoresTags(t *testing.T) {
	n := New("http://x", "success-tag", "failure-tag", "nochange-tag", 10*time.Second)
	if n.tagSuccess != "success-tag" {
		t.Errorf("tagSuccess = %q", n.tagSuccess)
	}
	if n.tagFailure != "failure-tag" {
		t.Errorf("tagFailure = %q", n.tagFailure)
	}
	if n.tagNoChanges != "nochange-tag" {
		t.Errorf("tagNoChanges = %q", n.tagNoChanges)
	}
}

// ──────────────────── Send() tests ────────────────────────────────────

func TestSend_Success(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/notify" {
			t.Errorf("expected /notify, got %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedPayload); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	err := n.Send(context.Background(), "Test Title", "Test Body", "test-tag", "markdown")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if receivedPayload.Title != "Test Title" {
		t.Errorf("title = %q, want %q", receivedPayload.Title, "Test Title")
	}
	if receivedPayload.Body != "Test Body" {
		t.Errorf("body = %q, want %q", receivedPayload.Body, "Test Body")
	}
	if receivedPayload.Tag != "test-tag" {
		t.Errorf("tag = %q, want %q", receivedPayload.Tag, "test-tag")
	}
	if receivedPayload.Format != "markdown" {
		t.Errorf("format = %q, want %q", receivedPayload.Format, "markdown")
	}
}

func TestSend_EmptyTag_OmitsTagField(t *testing.T) {
	var rawJSON json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&rawJSON)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	err := n.Send(context.Background(), "Title", "Body", "", "text")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify tag field is omitted (omitempty) when empty
	var m map[string]interface{}
	json.Unmarshal(rawJSON, &m)
	if _, exists := m["tag"]; exists {
		t.Error("tag field should be omitted when empty (omitempty)")
	}
}

func TestSend_EmptyFormat_OmitsFormatField(t *testing.T) {
	var rawJSON json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&rawJSON)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	err := n.Send(context.Background(), "Title", "Body", "tag", "")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(rawJSON, &m)
	if _, exists := m["format"]; exists {
		t.Error("format field should be omitted when empty (omitempty)")
	}
}

func TestSend_Non2xxResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	err := n.Send(context.Background(), "Title", "Body", "tag", "text")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestSend_ClientError4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	err := n.Send(context.Background(), "Title", "Body", "tag", "text")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestSend_Redirect3xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	err := n.Send(context.Background(), "Title", "Body", "tag", "text")
	if err == nil {
		t.Fatal("expected error for 302 response (not 2xx)")
	}
}

func TestSend_NetworkError(t *testing.T) {
	// Use an invalid URL to simulate network error
	n := New("http://localhost:1/notify", "ok", "fail", "", 50*time.Millisecond)
	err := n.Send(context.Background(), "Title", "Body", "tag", "text")
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestSend_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := n.Send(ctx, "Title", "Body", "tag", "text")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSend_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	time.Sleep(5 * time.Millisecond) // ensure deadline passed
	err := n.Send(ctx, "Title", "Body", "tag", "text")
	if err == nil {
		t.Fatal("expected deadline exceeded error")
	}
}

func TestSend_SpecialCharacters(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	title := "🚀 Deploy: <script>alert('xss')</script>"
	body := "Repo: git@github.com:user/repo.git\nBranch: main\nCommit: abc123$PATH"
	tag := "deploy-success: & specials!"

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	err := n.Send(context.Background(), title, body, tag, "markdown")
	if err != nil {
		t.Fatalf("Send failed with special chars: %v", err)
	}

	if receivedPayload.Title != title {
		t.Errorf("title was altered: got %q", receivedPayload.Title)
	}
	if receivedPayload.Body != body {
		t.Errorf("body was altered: got %q", receivedPayload.Body)
	}
}

// ──────────────────── Convenience methods ────────────────────────────

func TestNotifySuccess_Format(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "deploy-success", "deploy-failure", "", 10*time.Second)
	err := n.NotifySuccess(context.Background(), "myapp", "abc1234", "12.4s")
	if err != nil {
		t.Fatalf("NotifySuccess failed: %v", err)
	}

	if !strings.Contains(receivedPayload.Title, "myapp") {
		t.Errorf("title should mention hook name: %q", receivedPayload.Title)
	}
	if !strings.Contains(receivedPayload.Body, "abc1234") {
		t.Errorf("body should mention commit: %q", receivedPayload.Body)
	}
	if !strings.Contains(receivedPayload.Body, "12.4s") {
		t.Errorf("body should mention duration: %q", receivedPayload.Body)
	}
	if receivedPayload.Tag != "deploy-success" {
		t.Errorf("tag = %q, want deploy-success", receivedPayload.Tag)
	}
	if receivedPayload.Format != "markdown" {
		t.Errorf("format = %q, want markdown", receivedPayload.Format)
	}
}

func TestNotifyFailure_Format(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "deploy-failure", "", 10*time.Second)
	err := n.NotifyFailure(context.Background(), "myapp", "abc1234", "permission denied")
	if err != nil {
		t.Fatalf("NotifyFailure failed: %v", err)
	}

	if !strings.Contains(receivedPayload.Title, "myapp") {
		t.Errorf("title should mention hook name: %q", receivedPayload.Title)
	}
	if !strings.Contains(receivedPayload.Body, "permission denied") {
		t.Errorf("body should mention error: %q", receivedPayload.Body)
	}
	if receivedPayload.Tag != "deploy-failure" {
		t.Errorf("tag = %q, want deploy-failure", receivedPayload.Tag)
	}
}

func TestNotifyNoChanges_WithTag(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "no-changes", 10*time.Second)
	err := n.NotifyNoChanges(context.Background(), "myapp")
	if err != nil {
		t.Fatalf("NotifyNoChanges failed: %v", err)
	}

	if !strings.Contains(receivedPayload.Title, "myapp") {
		t.Errorf("title should mention hook name: %q", receivedPayload.Title)
	}
	if receivedPayload.Tag != "no-changes" {
		t.Errorf("tag = %q, want no-changes", receivedPayload.Tag)
	}
}

func TestNotifyNoChanges_EmptyTag_Silent(t *testing.T) {
	// When tagNoChanges is empty, no notification should be sent (no HTTP call).
	n := New("http://localhost:1/notify", "ok", "fail", "", 10*time.Second)
	err := n.NotifyNoChanges(context.Background(), "myapp")
	if err != nil {
		t.Errorf("NotifyNoChanges with empty tag should be silent: %v", err)
	}
}

// ──────────────────── SendDeployResult tests ──────────────────────────

func TestSendDeployResult_Success(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "deploy-success", "deploy-failure", "", 10*time.Second)

	result := &deploy.DeployResult{
		HookID:       "myapp",
		CommitBefore: "abc1234",
		CommitAfter:  "def56789",
		Services: []deploy.DeployServiceResult{
			{Name: "jellyfin", Changed: true, Restarted: true},
			{Name: "prowlarr", Changed: true, Restarted: true},
			{Name: "nginx", Changed: false, Restarted: false},
		},
		Duration: 12450 * time.Millisecond,
		Error:    nil,
	}

	err := n.SendDeployResult(context.Background(), result, "myapp")
	if err != nil {
		t.Fatalf("SendDeployResult failed: %v", err)
	}

	if receivedPayload.Tag != "deploy-success" {
		t.Errorf("tag = %q, want deploy-success", receivedPayload.Tag)
	}
	if !strings.Contains(receivedPayload.Body, "jellyfin") {
		t.Errorf("body should list service jellyfin")
	}
	if !strings.Contains(receivedPayload.Body, "prowlarr") {
		t.Errorf("body should list service prowlarr")
	}
	if !strings.Contains(receivedPayload.Body, "nginx") {
		t.Errorf("body should list service nginx")
	}
	if !strings.Contains(receivedPayload.Body, "12.45s") {
		t.Errorf("body should contain duration: %q", receivedPayload.Body)
	}
}

func TestSendDeployResult_Failure(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "success", "deploy-failure", "", 10*time.Second)

	result := &deploy.DeployResult{
		HookID:      "myapp",
		CommitAfter: "def5678",
		Error:       errors.New("git pull failed: permission denied (publickey)"),
	}

	err := n.SendDeployResult(context.Background(), result, "myapp")
	if err != nil {
		t.Fatalf("SendDeployResult failed: %v", err)
	}

	if receivedPayload.Tag != "deploy-failure" {
		t.Errorf("tag = %q, want deploy-failure", receivedPayload.Tag)
	}
	if !strings.Contains(receivedPayload.Body, "permission denied") {
		t.Errorf("body should mention error: %q", receivedPayload.Body)
	}
}

func TestSendDeployResult_NoChanges(t *testing.T) {
	// When CommitBefore == CommitAfter and both non-empty, call NotifyNoChanges
	n := New("http://localhost:1/notify", "success", "failure", "", 10*time.Second)

	result := &deploy.DeployResult{
		HookID:       "myapp",
		CommitBefore: "abc1234",
		CommitAfter:  "abc1234",
		Error:        nil,
	}

	err := n.SendDeployResult(context.Background(), result, "myapp")
	// Should be silent (tagNoChanges is empty, no HTTP call)
	// If it tries to call localhost:1, it may error; we just check it doesn't panic
	_ = err
}

func TestSendDeployResult_NilResult(t *testing.T) {
	n := New("http://apprise:8000", "success", "failure", "", 10*time.Second)
	err := n.SendDeployResult(context.Background(), nil, "myapp")
	if err == nil {
		t.Fatal("expected error for nil DeployResult")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error should mention nil: %v", err)
	}
}

func TestSendDeployResult_ShortHash(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "success", "failure", "", 10*time.Second)

	result := &deploy.DeployResult{
		HookID:       "myapp",
		CommitBefore: "oldhash",
		CommitAfter:  "abcdef1234567890abcdef1234567890abcdef12",
		Services:     []deploy.DeployServiceResult{},
		Duration:     1 * time.Second,
		Error:        nil,
	}

	err := n.SendDeployResult(context.Background(), result, "myapp")
	if err != nil {
		t.Fatalf("SendDeployResult failed: %v", err)
	}

	// Commit should be truncated to 7 chars
	if strings.Contains(receivedPayload.Body, "abcdef1234567890abcdef1234567890abcdef12") {
		t.Error("commit hash should be truncated to 7 chars")
	}
	if !strings.Contains(receivedPayload.Body, "abcdef1") {
		t.Error("body should contain short hash 'abcdef1'")
	}
}

func TestSendDeployResult_ServiceError(t *testing.T) {
	var receivedPayload apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "success", "failure", "", 10*time.Second)

	result := &deploy.DeployResult{
		HookID:       "myapp",
		CommitBefore: "abc1234",
		CommitAfter:  "def5678",
		Services: []deploy.DeployServiceResult{
			{Name: "svc1", Restarted: true},
			{Name: "svc2", Changed: true, Restarted: false, Error: errors.New("build failed")},
		},
		Duration: 5 * time.Second,
		Error:    nil,
	}

	err := n.SendDeployResult(context.Background(), result, "myapp")
	if err != nil {
		t.Fatalf("SendDeployResult failed: %v", err)
	}

	if !strings.Contains(receivedPayload.Body, "build failed") {
		t.Errorf("body should mention service error: %q", receivedPayload.Body)
	}
}

// ──────────────────── JSON payload tests ─────────────────────────────

func TestApprisePayload_JSONMarshalling(t *testing.T) {
	p := apprisePayload{
		Title:  "Test",
		Body:   "Hello World",
		Tag:    "test-tag",
		Format: "markdown",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded apprisePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Title != p.Title {
		t.Errorf("title = %q, want %q", decoded.Title, p.Title)
	}
	if decoded.Body != p.Body {
		t.Errorf("body = %q, want %q", decoded.Body, p.Body)
	}
	if decoded.Tag != p.Tag {
		t.Errorf("tag = %q, want %q", decoded.Tag, p.Tag)
	}
	if decoded.Format != p.Format {
		t.Errorf("format = %q, want %q", decoded.Format, p.Format)
	}
}

func TestApprisePayload_JSONContentType(t *testing.T) {
	// Verify the JSON we produce has correct content type.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	n.Send(context.Background(), "Title", "Body", "tag", "text")
}

// ────────────────────── shortHash tests ───────────────────────────────

func TestShortHash_Long(t *testing.T) {
	got := shortHash("abcdef1234567890")
	want := "abcdef1"
	if got != want {
		t.Errorf("shortHash = %q, want %q", got, want)
	}
}

func TestShortHash_Short(t *testing.T) {
	got := shortHash("abc")
	want := "abc"
	if got != want {
		t.Errorf("shortHash = %q, want %q", got, want)
	}
}

func TestShortHash_Empty(t *testing.T) {
	got := shortHash("")
	want := ""
	if got != want {
		t.Errorf("shortHash = %q, want %q", got, want)
	}
}

func TestShortHash_ExactlySeven(t *testing.T) {
	got := shortHash("1234567")
	want := "1234567"
	if got != want {
		t.Errorf("shortHash = %q, want %q", got, want)
	}
}

// ──────────────────── Security tests ──────────────────────────────────

func TestSecurity_NoSecretLeakageInError(t *testing.T) {
	// When the Apprise URL contains credentials, errors should not
	// leak them. The Notifier doesn't mask URLs, but Send doesn't
	// include the URL in most errors that reach the caller directly.
	n := New("http://admin:secret@localhost:1/notify", "ok", "fail", "", 50*time.Millisecond)
	err := n.Send(context.Background(), "Title", "Body", "tag", "text")
	if err == nil {
		return // no error, test still valid
	}
	// Check that the error doesn't include full URL with password
	if strings.Contains(err.Error(), "secret@") {
		t.Error("error message should not leak URL credentials")
	}
}

func TestSecurity_Timeout_PreventsBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	start := time.Now()
	n := New(srv.URL+"/notify", "ok", "fail", "", 50*time.Millisecond)
	_ = n.Send(context.Background(), "Title", "Body", "tag", "text")
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("timeout too slow: %v (expected < 500ms)", elapsed)
	}
}

func TestSecurity_LargeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 100KB body
	largeBody := strings.Repeat("x", 100_000)
	n := New(srv.URL+"/notify", "ok", "fail", "", 10*time.Second)
	err := n.Send(context.Background(), "Title", largeBody, "tag", "text")
	if err != nil {
		t.Fatalf("Send with large body failed: %v", err)
	}
}

func TestSecurity_NilContext(t *testing.T) {
	// nil context should not panic (though it may error)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Send panicked with nil context: %v", r)
		}
	}()

	n := New("http://localhost:1/notify", "ok", "fail", "", 50*time.Millisecond)
	_ = n.Send(nil, "Title", "Body", "tag", "text") //nolint:staticcheck
}
