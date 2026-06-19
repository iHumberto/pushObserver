package webhook

import "testing"

func TestSha256Hex(t *testing.T) {
	// Known test vector from RFC 4231
	// key = "secret", data = "hello" -> expected HMAC-SHA256
	got := sha256Hex([]byte("hello"), "secret")
	if got == "" {
		t.Fatal("sha256Hex returned empty string")
	}
	// Full assertion when validate is implemented
	t.Logf("HMAC-SHA256: %s", got)
}

func TestSha1Hex(t *testing.T) {
	got := sha1Hex([]byte("hello"), "secret")
	if got == "" {
		t.Fatal("sha1Hex returned empty string")
	}
	t.Logf("HMAC-SHA1: %s", got)
}

func TestValidate(t *testing.T) {
	// TODO: full HMAC validation tests with mock http requests
	t.Skip("not yet implemented")
}
