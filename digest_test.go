package netsec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDigestTextAndJSON(t *testing.T) {
	digest := SumDigest([]byte("weights"))
	text, err := digest.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(text) != digest.String() || len(text) != 64 {
		t.Fatalf("MarshalText = %q", text)
	}
	var parsed Digest
	if err := parsed.UnmarshalText(text); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if parsed != digest {
		t.Fatalf("round trip = %s, want %s", parsed, digest)
	}
	if _, err := ParseDigest("zz"); err == nil {
		t.Fatal("ParseDigest accepted invalid text")
	}

	encoded, err := json.Marshal(WeightManifest{GraphDigest: digest})
	if err != nil {
		t.Fatalf("Marshal manifest: %v", err)
	}
	if !strings.Contains(string(encoded), digest.String()) {
		t.Fatalf("manifest JSON does not use hexadecimal digest: %s", encoded)
	}
}

func TestManifestDigestIsStable(t *testing.T) {
	manifest := WeightManifest{
		GraphDigest: SumDigest([]byte("graph")),
		Files: []ManifestFile{{
			Location: "w.bin",
			Size:     4,
			Identity: FileIdentity{Device: 1, Inode: 2, Generation: 3, Size: 4},
		}},
		Tensors: []ManifestTensor{{
			TensorID: "t",
			Location: "w.bin",
			Length:   4,
			SHA256:   SumDigest([]byte{1, 2, 3, 4}),
			Dtype:    Uint8,
			Shape:    []int64{4},
		}},
	}
	first := manifest.Digest()
	second := manifest.Digest()
	if first != second || first.IsZero() {
		t.Fatalf("manifest digest = %s then %s", first, second)
	}
	manifest.GraphDigest[0] ^= 0xff
	if manifest.Digest() == first {
		t.Fatal("manifest digest did not bind graph digest")
	}
}
