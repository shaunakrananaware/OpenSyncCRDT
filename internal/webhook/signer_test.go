package webhook

import "testing"

func TestSignDeterministicAndKeyed(t *testing.T) {
	body := []byte(`{"event":"x"}`)
	a := Sign("secret", body)
	b := Sign("secret", body)
	if a != b {
		t.Errorf("signing not deterministic: %s != %s", a, b)
	}
	if Sign("other", body) == a {
		t.Errorf("different secrets produced the same signature")
	}
	// Known-answer: hex HMAC-SHA256 is 64 hex chars.
	if len(a) != 64 {
		t.Errorf("signature length = %d, want 64", len(a))
	}
}

func TestVerify(t *testing.T) {
	body := []byte("payload")
	sig := Sign("s3cr3t", body)
	if !Verify("s3cr3t", body, sig) {
		t.Error("valid signature failed to verify")
	}
	if Verify("s3cr3t", body, "deadbeef") {
		t.Error("invalid signature verified")
	}
	if Verify("wrong", body, sig) {
		t.Error("signature verified under wrong secret")
	}
}
