package webhook

// HMAC validation for GitHub (sha256/sha1), Forgejo/Gitea (sha256), GitLab (token), plain (dev).

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
)

// ErrInvalidSignature is returned when the HMAC signature does not match.
var ErrInvalidSignature = errors.New("invalid HMAC signature")

// Validate validates the HMAC signature of an incoming webhook request.
// TODO: implement — extract header, read body, compute HMAC, constant-time compare.
func Validate(r *http.Request, hmacType, secret, header string) error {
	_ = r
	_ = hmacType
	_ = secret
	_ = header
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
