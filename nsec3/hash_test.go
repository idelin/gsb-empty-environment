package nsec3

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

// RFC 5155 Appendix A standard hash test vectors (salt aabbccdd, 12
// iterations, SHA-1), also quoted verbatim on the RFC Editor errata page.
func TestRFC5155AppendixAVectors(t *testing.T) {
	salt, _ := hex.DecodeString("aabbccdd")
	cases := []struct {
		name string
		want string
	}{
		{"example.", "0p9mhaveqvm6t7vbl5lop2u3t2rp3tom"},
		{"a.example.", "35mthgpgcu1qg68fab165klnsnk3dpvl"},
		{"ai.example.", "gjeqe526plbf1g8mklp59enfd789njgi"},
		{"ns1.example.", "2t7b4g4vsa5smi47k61mv5bv1a22bojr"},
	}
	for _, c := range cases {
		got, err := HashName(HashSHA1, 12, salt, c.name)
		if err != nil {
			t.Fatalf("HashName(%q): %v", c.name, err)
		}
		if label := Base32HexEncode(got); label != c.want {
			t.Errorf("HashName(%q) = %s, want %s", c.name, label, c.want)
		}
		// The raw hash must round-trip through the base32hex owner label.
		dec, err := OwnerHashFromLabel(c.want)
		if err != nil {
			t.Fatalf("OwnerHashFromLabel(%q): %v", c.want, err)
		}
		if string(dec) != string(got) {
			t.Errorf("label round trip mismatch for %s", c.name)
		}
	}
}

// Case and trailing-dot insensitivity: the presentation form is
// lower-cased before hashing and the trailing dot is optional.
func TestCanonicalNameHashing(t *testing.T) {
	salt, _ := hex.DecodeString("aabbccdd")
	canonical, err := HashName(HashSHA1, 12, salt, "ns1.example.")
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"NS1.EXAMPLE.", "Ns1.Example", "ns1.example"} {
		got, err := HashName(HashSHA1, 12, salt, in)
		if err != nil {
			t.Fatalf("HashName(%q): %v", in, err)
		}
		if string(got) != string(canonical) {
			t.Errorf("HashName(%q) not canonical", in)
		}
	}
}

// The chain helper must expose H(0)..H(iterations) and its final element
// must equal the one-shot hash (RFC 5155 section 5 iteration formula).
func TestHashChainShape(t *testing.T) {
	salt, _ := hex.DecodeString("aabbccdd")
	chain, err := HashNameChain(HashSHA1, 12, salt, "a.example.")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 13 || len(chain[0]) != 20 {
		t.Fatalf("chain shape = %d entries of %d bytes", len(chain), len(chain[0]))
	}
	final, err := HashName(HashSHA1, 12, salt, "a.example.")
	if err != nil {
		t.Fatal(err)
	}
	if string(chain[12]) != string(final) {
		t.Fatal("chain tail does not match HashName")
	}
	// Each successive iteration is SHA-1(prev || salt), checked directly.
	for k := 1; k < len(chain); k++ {
		step := sha1.Sum(append(append([]byte{}, chain[k-1]...), salt...))
		if string(step[:]) != string(chain[k]) {
			t.Fatalf("iteration %d formula violated", k)
		}
	}
}

func TestHashRejections(t *testing.T) {
	if _, err := HashName(2, 0, nil, "example."); err != ErrUnknownAlgorithm {
		t.Errorf("unknown algorithm: got %v, want ErrUnknownAlgorithm", err)
	}
	if _, err := HashName(HashSHA1, MaxIterations+1, nil, "example."); err != ErrIterationsTooLarge {
		t.Errorf("iterations: got %v, want ErrIterationsTooLarge", err)
	}
	if _, err := HashName(HashSHA1, MaxIterations, nil, "example."); err != nil {
		t.Errorf("iterations at limit rejected: %v", err)
	}
	if _, err := HashName(HashSHA1, 0, nil, "bad..name."); err != ErrInvalidName {
		t.Errorf("bad name: got %v, want ErrInvalidName", err)
	}
}

func TestIterationFormula(t *testing.T) {
	// H(k) = SHA-1(H(k-1) || salt): cross-check the chain helper against
	// a direct loop for arbitrary parameters.
	salt := []byte{1, 2, 3, 4}
	chain, err := HashNameChain(HashSHA1, 5, salt, "host.example.org.")
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := WireName("host.example.org.")
	direct := iterateHash(wire, 5, salt)
	if string(direct) != string(chain[5]) {
		t.Fatalf("formula mismatch:\nchain=%x\ndirect=%x", chain[5], direct)
	}
}

func TestZeroIterationsEmptySalt(t *testing.T) {
	// With iterations=0 and an empty salt, H is just SHA-1(wire name).
	wire, err := WireName("example.")
	if err != nil {
		t.Fatal(err)
	}
	want := sha1.Sum(wire)
	got, err := HashName(HashSHA1, 0, nil, "example.")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want[:]) {
		t.Fatalf("zero-iteration hash = %x, want %x", got, want)
	}
	chain, err := HashNameChain(HashSHA1, 0, nil, "example.")
	if err != nil || len(chain) != 1 || string(chain[0]) != string(want[:]) {
		t.Fatalf("zero-iteration chain = %v, %v", chain, err)
	}
}

func TestRootNameHash(t *testing.T) {
	// The root wire name is a single zero octet; it must hash cleanly.
	h, err := HashName(HashSHA1, 0, []byte{1}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 20 {
		t.Fatalf("root hash length = %d", len(h))
	}
}
