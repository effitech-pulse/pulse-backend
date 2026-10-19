package telemetry

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSecretsAuthenticatedEncryption(t *testing.T) {
	s := Secrets{Key: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))}
	a, err := s.Seal("token-test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Seal("token-test")
	if err != nil || a == b {
		t.Fatal("nonce reused")
	}
	plain, err := s.Open(a)
	if err != nil || plain != "token-test" {
		t.Fatal("roundtrip failed")
	}
	raw, _ := base64.StdEncoding.DecodeString(a)
	raw[len(raw)-1] ^= 1
	if _, err = s.Open(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err = (Secrets{}).Seal("secret"); err == nil {
		t.Fatal("missing key accepted")
	}
}
func TestAllowedURLRequiresExactHost(t *testing.T) {
	if _, err := AllowedURL("http://example.test.evil/", []string{"example.test"}); err == nil {
		t.Fatal("host suffix bypass")
	}
	if _, err := AllowedURL("http://reader:password@example.test/", []string{"example.test"}); err == nil {
		t.Fatal("credential URL accepted")
	}
	if _, err := AllowedURL("https://example.test/api", []string{"example.test"}); err != nil {
		t.Fatal(err)
	}
}
