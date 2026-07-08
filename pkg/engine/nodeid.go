package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// generateNodeID returns a random identifier for this instance, used in
// /api/v1/nodes.
func generateNodeID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	return "node-" + hex.EncodeToString(b[:])
}
