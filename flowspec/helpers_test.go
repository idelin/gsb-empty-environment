package flowspec

import (
	"encoding/hex"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func mustDecodeComponents(t *testing.T, s string) []Component {
	t.Helper()
	cs, err := DecodeComponents(mustHex(t, s), AFIPv4)
	if err != nil {
		t.Fatalf("DecodeComponents failed: %v\nhex=%s", err, s)
	}
	return cs
}
