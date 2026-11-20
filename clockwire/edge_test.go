package clockwire

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestSignedLegacyBound(t *testing.T) {
	raw, _ := hex.DecodeString("0100030001fffffffffffffffe0000000000000005")
	z, e := DecodeV1(raw)
	if e != nil || z != (Bound{3, 1, -2, 5}) {
		t.Fatalf("%v %v", z, e)
	}
	out, e := z.EncodeV1()
	if e != nil || !bytes.Equal(out, raw) {
		t.Fatal("v1 encoding changed")
	}
}
