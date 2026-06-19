// Package webhook handles incoming webhook POST requests and HMAC validation.
//
// Supported platforms: GitHub (sha256, sha1 legacy), Forgejo/Gitea (sha256), GitLab (token).
// Dev mode supports plain (no HMAC).
package webhook

// TODO: Handler struct, ServeHTTP, parsePayload, HMAC validation
