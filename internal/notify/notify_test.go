package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forgejo.humbertof.dev/humberto/push-observer/internal/deploy"
)

// ─────────────────────────────── Helpers ──────────────────────────────────

// newTestNotifier creates a Notifier pointing at a test Apprise server.
func newTestNotifier(serverURL string) *Notifier {
	return New(serverURL, "deploy-success", "deploy-failure", "", 10*time.Second)
}

// newTestNotifierWithNoopTag creates a Notifier with a no-changes tag.
func newTestNotifierWithNoopTag(serverURL string) *Notifier {
	return New(serverURL, "deploy-success", "deploy-failure", "no-changes", 10*time.Second)
}

// decodePayload reads and decodes the JSON body from an HTTP request.
func decodePayload(t *testing.T, r *http.Request) apprisePayload {
	t.Helper()
	var p apprisePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		t.Fatalf("decode apprise payload: %v", err)
	}
	return p
}

// ──────────────────────── apprisePayload tests ────────────────────────────

func TestApprisePayload_JSONMarshal(t *testing.T) {
	p := apprisePayload{
		Title:  "Test",
		Body:   "Body here",
		Tag:    "deploy-success",
		Format: "markdown",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded apprisePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Title != "Test" {
		t.Errorf("title = %q, want %q", decoded.Title, "Test")
	}
	if decoded.Body != "Body here" {
		t.Errorf("body = %q, want %q", decoded.Body, "Body here")
	}
	if decoded.Tag != "deploy-success" {
		t.Errorf("tag = %q, want %q", decoded.Tag, "deploy-success")
	}
	if decoded.Format != "markdown" {
		t.Errorf("format = %q, want %q", decoded.Format, "markdown")
	}
}

func TestApprisePayload_OmitEmptyTag(t *testing.T) {
	p := apprisePayload{
		Title: "Test",
		Body:  "Body",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Tag should be omitted when empty
	if strings.Contains(string(data), `"tag"`) {
		t.Error("empty tag should be omitted from JSON")
	}
}

// ─────────────────────── Send tests ───────────────────────────────────────

func TestSend_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		p := decodePayload(t, r)
		if p.Title != "Test Title" {
			t.Errorf("title = %q, want %q", p.Title, "Test Title")
		}
		if p.Body != "Test Body" {
			t.Errorf("body = %q, want %q", p.Body, "Test Body")
		}
		if p.Tag != "deploy-success" {
			t.Errorf("tag = %q, want %q", p.Tag, "deploy-success")
		}
		if p.Format != "markdown" {
			t.Errorf("format = %q, want %q", p.Format, "markdown")
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)
	err := notifier.Send(context.Background(), "Test Title", "Test Body", "deploy-success", "markdown")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSend_FormatText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Format != "text" {
			t.Errorf("format = %q, want text", p.Format)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)
	if err := notifier.Send(context.Background(), "T", "B", "deploy-failure", "text"); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSend_TagFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "deploy-failure" {
			t.Errorf("tag = %q, want deploy-failure", p.Tag)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)
	if err := notifier.Send(context.Background(), "T", "B", "deploy-failure", "markdown"); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSend_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)
	err := notifier.Send(context.Background(), "T", "B", "deploy-success", "markdown")
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status 500, got: %v", err)
	}
}

func TestSend_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)
	err := notifier.Send(context.Background(), "T", "B", "deploy-success", "markdown")
	if err == nil {
		t.Fatal("expected error on 404 response")
	}
}

func TestSend_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	notifier := newTestNotifier(server.URL)
	err := notifier.Send(ctx, "T", "B", "deploy-success", "markdown")
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestSend_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := New(server.URL, "deploy-success", "deploy-failure", "", 50*time.Millisecond)
	err := notifier.Send(context.Background(), "T", "B", "deploy-success", "markdown")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "notify: send to apprise") {
		t.Errorf("error should mention notify: send to apprise, got: %v", err)
	}
}

func TestSend_InvalidURL(t *testing.T) {
	notifier := New("http://[::1]:namedport", "deploy-success", "deploy-failure", "", 10*time.Second)
	err := notifier.Send(context.Background(), "T", "B", "deploy-success", "markdown")

	// Go 1.24 changed URL parsing behavior; accept either request creation or transport error.
	if err == nil {
		t.Fatal("expected error with invalid URL")
	}
}

func TestSend_EmptyTitleAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)
	if err := notifier.Send(context.Background(), "", "", "deploy-success", "markdown"); err != nil {
		t.Fatalf("empty title/body should be valid: %v", err)
	}
}

// ─────────────────── Convenience method tests ─────────────────────────────

func TestNotifySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "deploy-success" {
			t.Errorf("tag = %q, want deploy-success", p.Tag)
		}
		if p.Format != "markdown" {
			t.Errorf("format = %q, want markdown", p.Format)
		}
		if !strings.Contains(p.Title, "✅ Deploy: myhook") {
			t.Errorf("title should contain hook name, got %q", p.Title)
		}
		if !strings.Contains(p.Body, "myhook") {
			t.Errorf("body should contain hook name, got %q", p.Body)
		}
		if !strings.Contains(p.Body, "abc1234") {
			t.Errorf("body should contain commit, got %q", p.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)
	if err := notifier.NotifySuccess(context.Background(), "myhook", "abc1234", "2.5s"); err != nil {
		t.Fatalf("NotifySuccess: %v", err)
	}
}

func TestNotifyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "deploy-failure" {
			t.Errorf("tag = %q, want deploy-failure", p.Tag)
		}
		if !strings.Contains(p.Title, "❌ Deploy failed: myhook") {
			t.Errorf("title should indicate failure, got %q", p.Title)
		}
		if !strings.Contains(p.Body, "something broke") {
			t.Errorf("body should contain error message, got %q", p.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)
	if err := notifier.NotifyFailure(context.Background(), "myhook", "abc1234", "something broke"); err != nil {
		t.Fatalf("NotifyFailure: %v", err)
	}
}

func TestNotifyNoChanges_WithTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "no-changes" {
			t.Errorf("tag = %q, want no-changes", p.Tag)
		}
		if !strings.Contains(p.Title, "No changes") {
			t.Errorf("title should indicate no changes, got %q", p.Title)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newTestNotifierWithNoopTag(server.URL)
	if err := notifier.NotifyNoChanges(context.Background(), "myhook"); err != nil {
		t.Fatalf("NotifyNoChanges: %v", err)
	}
}

func TestNotifyNoChanges_EmptyTag_Silent(t *testing.T) {
	// No server needed — should not send any request
	notifier := newTestNotifier("http://127.0.0.1:1") // unreachable
	err := notifier.NotifyNoChanges(context.Background(), "myhook")
	if err != nil {
		t.Fatalf("NotifyNoChanges with empty tag should be silent: %v", err)
	}
}

func TestNotifyNoChanges_EmptyTag_NoHTTPCall(t *testing.T) {
	// Same as above but more explicit: the function returns nil without HTTP call
	notifier := newTestNotifier("http://127.0.0.1:1")
	err := notifier.NotifyNoChanges(context.Background(), "myhook")
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// ─────────────────── New / constructor tests ──────────────────────────────

func TestNew_Defaults(t *testing.T) {
	n := New("http://apprise:8000/notify", "success", "failure", "", 0)
	if n.client.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", n.client.Timeout)
	}
	if n.tagSuccess != "success" {
		t.Errorf("tagSuccess = %q, want success", n.tagSuccess)
	}
	if n.tagFailure != "failure" {
		t.Errorf("tagFailure = %q, want failure", n.tagFailure)
	}
	if n.appriseURL != "http://apprise:8000/notify" {
		t.Errorf("appriseURL = %q", n.appriseURL)
	}
}

func TestNew_CustomTimeout(t *testing.T) {
	n := New("http://apprise:8000/notify", "s", "f", "", 5*time.Second)
	if n.client.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", n.client.Timeout)
	}
}

func TestNew_NegativeTimeout_DefaultsTo10s(t *testing.T) {
	n := New("http://apprise:8000/notify", "s", "f", "", -1*time.Second)
	if n.client.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s (default)", n.client.Timeout)
	}
}

// ─────────────────── shortHash tests ──────────────────────────────────────

func TestShortHash_Full(t *testing.T) {
	if got := shortHash("a1b2c3d4e5f6"); got != "a1b2c3d" {
		t.Errorf("shortHash = %q, want a1b2c3d", got)
	}
}

func TestShortHash_Short(t *testing.T) {
	if got := shortHash("abc"); got != "abc" {
		t.Errorf("shortHash = %q, want abc", got)
	}
}

func TestShortHash_Empty(t *testing.T) {
	if got := shortHash(""); got != "" {
		t.Errorf("shortHash = %q, want empty", got)
	}
}

func TestShortHash_Exactly7(t *testing.T) {
	if got := shortHash("1234567"); got != "1234567" {
		t.Errorf("shortHash = %q, want 1234567", got)
	}
}

// ─────────────────── SendDeployResult tests ───────────────────────────────

func TestSendDeployResult_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "deploy-success" {
			t.Errorf("tag = %q, want deploy-success", p.Tag)
		}
		if !strings.Contains(p.Body, "api") {
			t.Error("body should mention service api")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := &deploy.DeployResult{
		HookID:       "myhook",
		CommitBefore: "aaaaaaa",
		CommitAfter:  "bbbbbbbbbbbbbbbb",
		Services: []deploy.DeployServiceResult{
			{Name: "api", Restarted: true, Changed: true, Reason: "restart-triggered"},
			{Name: "web", Restarted: false, Changed: false, Reason: "no-changes"},
		},
		Duration: 2 * time.Second,
	}

	notifier := newTestNotifier(server.URL)
	if err := notifier.SendDeployResult(context.Background(), result, "myhook"); err != nil {
		t.Fatalf("SendDeployResult: %v", err)
	}
}

func TestSendDeployResult_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "deploy-failure" {
			t.Errorf("tag = %q, want deploy-failure", p.Tag)
		}
		if !strings.Contains(p.Body, "git clone failed") {
			t.Errorf("body should contain error, got %q", p.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := &deploy.DeployResult{
		HookID:      "myhook",
		CommitAfter: "bbbbbbbbbbbbbbbb",
		Error:       errors.New("git clone failed"),
		Duration:    1 * time.Second,
	}

	notifier := newTestNotifier(server.URL)
	if err := notifier.SendDeployResult(context.Background(), result, "myhook"); err != nil {
		t.Fatalf("SendDeployResult failure: %v", err)
	}
}

func TestSendDeployResult_NoChanges(t *testing.T) {
	notifierCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notifierCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := &deploy.DeployResult{
		HookID:       "myhook",
		CommitBefore: "aaaabbbbccccddddeeeeffff",
		CommitAfter:  "aaaabbbbccccddddeeeeffff", // same commit
		Duration:     500 * time.Millisecond,
	}

	// Without a no-changes tag, it should be silent (no HTTP call)
	notifier := newTestNotifier(server.URL)
	if err := notifier.SendDeployResult(context.Background(), result, "myhook"); err != nil {
		t.Fatalf("SendDeployResult no-changes: %v", err)
	}

	if notifierCalled {
		t.Error("NotifyNoChanges with empty tag should not make HTTP call")
	}
}

func TestSendDeployResult_NoChanges_WithTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "no-changes" {
			t.Errorf("tag = %q, want no-changes", p.Tag)
		}
		if !strings.Contains(p.Body, "Nothing to deploy") {
			t.Errorf("body should mention nothing to deploy, got %q", p.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := &deploy.DeployResult{
		HookID:       "myhook",
		CommitBefore: "aaaabbbbccccddddeeeeffff",
		CommitAfter:  "aaaabbbbccccddddeeeeffff",
		Duration:     500 * time.Millisecond,
	}

	notifier := newTestNotifierWithNoopTag(server.URL)
	if err := notifier.SendDeployResult(context.Background(), result, "myhook"); err != nil {
		t.Fatalf("SendDeployResult no-changes with tag: %v", err)
	}
}

func TestSendDeployResult_FirstDeploy(t *testing.T) {
	// First deploy: CommitBefore is empty, so it should be success even if no services changed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "deploy-success" {
			t.Errorf("first deploy should use success tag, got %q", p.Tag)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := &deploy.DeployResult{
		HookID:       "myhook",
		CommitBefore: "", // first deploy
		CommitAfter:  "bbbbbbbbbbbbbbbb",
		Services: []deploy.DeployServiceResult{
			{Name: "api", Restarted: true, Changed: true},
		},
		Duration: 3 * time.Second,
	}

	notifier := newTestNotifier(server.URL)
	if err := notifier.SendDeployResult(context.Background(), result, "myhook"); err != nil {
		t.Fatalf("SendDeployResult first deploy: %v", err)
	}
}

func TestSendDeployResult_PartialFailure(t *testing.T) {
	// Some services failed individually, but the deploy pipeline itself didn't error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "deploy-success" {
			t.Errorf("partial failure should still use success tag, got %q", p.Tag)
		}
		if !strings.Contains(p.Body, "failed ❌") {
			t.Error("body should report failed services")
		}
		if !strings.Contains(p.Body, "restarted ✅") {
			t.Error("body should report restarted services")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := &deploy.DeployResult{
		HookID:       "myhook",
		CommitBefore: "aaaaaaa",
		CommitAfter:  "bbbbbbbbbbbbbbbb",
		Services: []deploy.DeployServiceResult{
			{Name: "api", Restarted: true, Changed: true, Reason: "restart-triggered"},
			{Name: "db", Restarted: false, Changed: true, Error: errors.New("compose up failed"), Reason: "restart-triggered"},
			{Name: "web", Restarted: false, Changed: false, Reason: "no-changes"},
		},
		Duration: 1 * time.Second,
	}

	notifier := newTestNotifier(server.URL)
	if err := notifier.SendDeployResult(context.Background(), result, "myhook"); err != nil {
		t.Fatalf("SendDeployResult partial failure: %v", err)
	}
}

func TestSendDeployResult_Nil(t *testing.T) {
	notifier := newTestNotifier("http://127.0.0.1:1")
	err := notifier.SendDeployResult(context.Background(), nil, "myhook")
	if err == nil {
		t.Fatal("expected error for nil DeployResult")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error should mention nil, got: %v", err)
	}
}

func TestSendDeployResult_EmptyServices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := decodePayload(t, r)
		if p.Tag != "deploy-success" {
			t.Errorf("tag = %q, want deploy-success", p.Tag)
		}
		// Body should not have per-service details since there are no services
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result := &deploy.DeployResult{
		HookID:       "myhook",
		CommitBefore: "aaaaaaa",
		CommitAfter:  "bbbbbbbbbbbbbbbb",
		Services:     nil,
		Duration:     1 * time.Second,
	}

	notifier := newTestNotifier(server.URL)
	if err := notifier.SendDeployResult(context.Background(), result, "myhook"); err != nil {
		t.Fatalf("SendDeployResult empty services: %v", err)
	}
}

// ─────────────────────── Full integration test ─────────────────────────────

func TestFullFlow_Success_ReadsPayload(t *testing.T) {
	// Full integration test: simulate the entire notify flow end-to-end.
	var receivedPayload apprisePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newTestNotifier(server.URL)

	// 1. Success
	if err := notifier.NotifySuccess(context.Background(), "myapp", "abc1234", "3s"); err != nil {
		t.Fatalf("NotifySuccess: %v", err)
	}
	if receivedPayload.Tag != "deploy-success" {
		t.Errorf("success tag = %q", receivedPayload.Tag)
	}

	// 2. Failure
	if err := notifier.NotifyFailure(context.Background(), "myapp", "def5678", "timeout"); err != nil {
		t.Fatalf("NotifyFailure: %v", err)
	}
	if receivedPayload.Tag != "deploy-failure" {
		t.Errorf("failure tag = %q", receivedPayload.Tag)
	}
}
