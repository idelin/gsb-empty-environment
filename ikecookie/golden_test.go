package ikecookie

import (
	"encoding/hex"
	"net/netip"
	"testing"
)

// TestGoldenVector pins the exact framed HMAC construction so an accidental
// change to the cookie message layout is caught. Inputs are fully fixed.
func TestGoldenVector(t *testing.T) {
	secret := make([]byte, secretSize)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	req := InitRequest{
		SourceIP: netip.MustParseAddr("198.51.100.7"),
		SPIi:     spiiAt(0xCD),
		Ni:       []byte("golden-ni"),
	}

	got := hex.EncodeToString((&Admitter{}).computeCookieLocked(secret, req))
	const want = "640c18c789cd230f17ea169c80a124317c4a67368801a2ee811def3728d839a2"
	if got != want {
		t.Fatalf("cookie framing changed:\n got %s\nwant %s", got, want)
	}
}
