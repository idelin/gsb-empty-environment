package pkcs12verify

import (
	"encoding/asn1"
	"testing"
)

// rewriteLegacyIterations re-encodes a valid PFX after overwriting the
// MacData iteration count. The MAC is left stale on purpose: the iteration
// policy gate must reject before (or instead of) doing KDF work, so no
// expensive derivation at 1e8 iterations is ever performed.
func rewriteLegacyIterations(t *testing.T, der []byte, iterations int) []byte {
	t.Helper()
	var pfx pfxPdu
	if _, err := asn1.Unmarshal(der, &pfx); err != nil {
		t.Fatal(err)
	}
	pfx.MacData.Iterations = iterations
	return mustMarshal(pfx)
}
