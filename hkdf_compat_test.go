package tls13

import (
	"crypto"
	"testing"
)

func TestHKDFExpandLabelAcceptsCryptoHash(t *testing.T) {
	secret := mustHex(t, "b6 7b 7d 69 0c c1 6c 4e 75 e5 42 13 cb 2d 37 b4 e9 c9 12 bc de d9 10 5d 42 be fd 59 d3 91 ad 38")
	got := HKDFExpandLabel(crypto.SHA256, secret, "key", nil, 16)
	assertHex(t, "key via crypto.Hash", got, `3f ce 51 60 09 c2 17 27 d0 f2 e4 e8 6e e4 03 bc`)
}
