package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 — SHA1 HMAC intentionally supported for legacy services (older GitLab/Gitea).
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrInvalidSignature is returned when the HMAC signature does not match.
var ErrInvalidSignature = errors.New("invalid HMAC signature")

// ErrMissingHeader is returned when the expected HMAC header is absent.
var ErrMissingHeader = errors.New("missing HMAC signature header")

// Validate validates the HMAC signature of an incoming webhook request.
//
// Supported types:
//   - sha256: GitHub/Forgejo/Gitea format "sha256=<hex>" in the configured header
//   - sha1:   GitHub legacy format "sha1=<hex>" in the configured header
//   - token:  GitLab plain token comparison (constant-time)
//   - plain:  No validation (dev mode only)
//
// The request body is preserved — after validation it can still be read
// by downstream handlers.
func Validate(r *http.Request, hmacType, secret, header string) error {
	if r == nil {
		return errors.New("nil request")
	}

	if hmacType == "plain" || hmacType == "" {
		return nil
	}

	// Read the full body.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("cannot read request body: %w", err)
	}

	// Restore the body for downstream handlers.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	switch hmacType {
	case "sha256":
		return validateSHA256(bodyBytes, secret, r.Header.Get(header), header)
	case "sha1":
		return validateSHA1(bodyBytes, secret, r.Header.Get(header), header)
	case "token":
		return validateToken(bodyBytes, secret, r.Header.Get(header), header)
	default:
		return fmt.Errorf("unknown HMAC type: %q", hmacType)
	}
}

// validateSHA256 validates a SHA256 HMAC signature.
// Expects header format: "sha256=<hex>".
func validateSHA256(payload []byte, secret, signature, headerName string) error {
	if signature == "" {
		return fmt.Errorf("%w: %s header is empty", ErrMissingHeader, headerName)
	}

	sig := strings.TrimPrefix(signature, "sha256=")
	if sig == signature {
		return fmt.Errorf("%w: expected 'sha256=' prefix in %s", ErrInvalidSignature, headerName)
	}

	expected := sha256Hex(payload, secret)
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return ErrInvalidSignature
	}
	return nil
}

// validateSHA1 validates a SHA1 HMAC signature.
// Expects header format: "sha1=<hex>".
func validateSHA1(payload []byte, secret, signature, headerName string) error {
	if signature == "" {
		return fmt.Errorf("%w: %s header is empty", ErrMissingHeader, headerName)
	}

	sig := strings.TrimPrefix(signature, "sha1=")
	if sig == signature {
		return fmt.Errorf("%w: expected 'sha1=' prefix in %s", ErrInvalidSignature, headerName)
	}

	expected := sha1Hex(payload, secret)
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return ErrInvalidSignature
	}
	return nil
}

// validateToken validates a plain token (GitLab-style).
// Compares the token in constant time.
func validateToken(payload []byte, secret, token, headerName string) error {
	_ = payload
	if secret == "" {
		return fmt.Errorf("%w: %s header is empty", ErrMissingHeader, headerName)
	}
	if !hmac.Equal([]byte(secret), []byte(token)) {
		return ErrInvalidSignature
	}
	return nil
}

// sha256Hex computes HMAC-SHA256 and returns the hex-encoded result.
func sha256Hex(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// sha1Hex computes HMAC-SHA1 and returns the hex-encoded result.
func sha1Hex(payload []byte, secret string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
