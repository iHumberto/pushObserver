// Package notify sends deployment notifications via Apprise HTTP API.
//
// Apprise is a separate container that receives POST /notify and forwards to
// Discord, Telegram, ntfy, Slack, and 100+ other services.
// pushObserver only needs to know one URL: http://apprise:8000/notify.
//
// JSON payload format: {"title": "...", "body": "...", "tag": "...", "format": "markdown"}
// Tags: deploy-success, deploy-failure (configured via config.Notifications).
// Timeout: 10s default.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/HumbertoF28/push-observer/internal/deploy"
)

// ─────────────────────────────── Types ──────────────────────────────────

// Notifier sends deployment notifications via Apprise HTTP API.
// It holds the Apprise URL, HTTP client with timeout, and tag configuration.
type Notifier struct {
	appriseURL   string
	client       *http.Client
	tagSuccess   string
	tagFailure   string
	tagNoChanges string
}

// apprisePayload is the JSON body sent to Apprise POST /notify.
type apprisePayload struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Tag    string `json:"tag,omitempty"`
	Format string `json:"format,omitempty"`
}

// ───────────────────────── Constructor ──────────────────────────────────

// New creates a new Notifier with the given Apprise URL and timeout.
//
//	tagSuccess:   tag for successful deploys (e.g. "deploy-success")
//	tagFailure:   tag for failed deploys (e.g. "deploy-failure")
//	tagNoChanges: tag for no-changes notifications (empty = silent)
func New(appriseURL, tagSuccess, tagFailure, tagNoChanges string, timeout time.Duration) *Notifier {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Notifier{
		appriseURL:   appriseURL,
		client:       &http.Client{Timeout: timeout},
		tagSuccess:   tagSuccess,
		tagFailure:   tagFailure,
		tagNoChanges: tagNoChanges,
	}
}

// ───────────────────── Core Send ────────────────────────────────────────

// Send sends a notification to Apprise via POST /notify.
// Returns nil on HTTP 2xx, or an error on network failure, timeout, or non-2xx response.
func (n *Notifier) Send(ctx context.Context, title, body, tag, format string) error {
	payload := apprisePayload{
		Title:  title,
		Body:   body,
		Tag:    tag,
		Format: format,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.appriseURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("notify: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send to apprise: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: apprise returned status %d", resp.StatusCode)
	}

	return nil
}

// ─────────────────── Convenience methods ─────────────────────────────────

// NotifySuccess sends a deploy-success notification.
func (n *Notifier) NotifySuccess(ctx context.Context, hookName, commit, duration string) error {
	title := fmt.Sprintf("✅ Deploy: %s", hookName)
	body := fmt.Sprintf("Deploy of **%s** completed successfully.\n\nCommit: `%s`\nDuration: %s",
		hookName, commit, duration)
	return n.Send(ctx, title, body, n.tagSuccess, "markdown")
}

// NotifyFailure sends a deploy-failure notification.
func (n *Notifier) NotifyFailure(ctx context.Context, hookName, commit, errMsg string) error {
	title := fmt.Sprintf("❌ Deploy failed: %s", hookName)
	body := fmt.Sprintf("Deploy of **%s** failed.\n\nCommit: `%s`\nError: %s",
		hookName, commit, errMsg)
	return n.Send(ctx, title, body, n.tagFailure, "markdown")
}

// NotifyNoChanges sends a no-changes notification.
// If tagNoChanges is empty, the notification is silently dropped.
func (n *Notifier) NotifyNoChanges(ctx context.Context, hookName string) error {
	if n.tagNoChanges == "" {
		return nil
	}
	title := fmt.Sprintf("ℹ️ No changes: %s", hookName)
	body := fmt.Sprintf("No changes detected for **%s**. Nothing to deploy.", hookName)
	return n.Send(ctx, title, body, n.tagNoChanges, "markdown")
}

// ─────────────────── SendDeployResult ────────────────────────────────────

// shortHash returns the first 7 characters of a git hash.
func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// SendDeployResult formats a DeployResult and sends the appropriate notification.
//
// Decision matrix:
//   - result.Error != nil → NotifyFailure
//   - no new commits (CommitBefore == CommitAfter, both non-empty) → NotifyNoChanges
//   - otherwise → NotifySuccess (with per-service details)
func (n *Notifier) SendDeployResult(ctx context.Context, result *deploy.DeployResult, hookName string) error {
	if result == nil {
		return fmt.Errorf("notify: DeployResult is nil")
	}

	commit := shortHash(result.CommitAfter)
	duration := result.Duration.Truncate(time.Millisecond).String()

	// Failure: deploy engine returned an error
	if result.Error != nil {
		return n.NotifyFailure(ctx, hookName, commit, result.Error.Error())
	}

	// No changes: before and after commits are identical (no new code pulled)
	if result.CommitBefore != "" && result.CommitBefore == result.CommitAfter {
		return n.NotifyNoChanges(ctx, hookName)
	}

	// Build per-service details
	var details strings.Builder
	for _, svc := range result.Services {
		switch {
		case svc.Restarted:
			fmt.Fprintf(&details, "- **%s**: restarted ✅\n", svc.Name)
		case svc.Error != nil:
			fmt.Fprintf(&details, "- **%s**: failed ❌ (%s)\n", svc.Name, svc.Error)
		case svc.Changed:
			fmt.Fprintf(&details, "- **%s**: build/restart triggered but failed ⚠️\n", svc.Name)
		default:
			details.WriteString(fmt.Sprintf("- **%s**: no changes\n", svc.Name))
		}
	}

	title := fmt.Sprintf("✅ Deploy: %s", hookName)
	body := fmt.Sprintf("Deploy of **%s** completed.\n\nCommit: `%s`\nDuration: %s\n\nServices:\n%s",
		hookName, commit, duration, details.String())

	return n.Send(ctx, title, body, n.tagSuccess, "markdown")
}
