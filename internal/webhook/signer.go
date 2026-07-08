// Package webhook provides HMAC signing and outbound event dispatch shared by
// the auth webhook, the conflict-resolver webhook, and the event webhooks.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Sign returns the hex-encoded HMAC-SHA256 of body keyed by secret. This is the
// value sent in the X-OpenSyncCRDT-Signature header on every outbound request.
// An empty secret still produces a (weak) signature so verification code paths
// stay uniform; developers are expected to configure a secret.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether sig is a valid hex HMAC-SHA256 of body under secret,
// using a constant-time comparison.
func Verify(secret string, body []byte, sig string) bool {
	expected := Sign(secret, body)
	return hmac.Equal([]byte(expected), []byte(sig))
}
