package arbiter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"cacaoferment/task"
)

// CredentialDigest produces a deterministic digest over the terminal outcome,
// bound to the task, generation, winning operation key, and issue tick. It is
// embedded in the final credential so the terminal record is tamper-evident.
func CredentialDigest(taskID string, generation int64, state task.State, operationKey string, tick int64) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%d", taskID, generation, string(state), operationKey, tick)
	return hex.EncodeToString(h.Sum(nil))
}
