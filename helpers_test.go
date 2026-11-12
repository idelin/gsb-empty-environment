package taskpkg_test

import (
	"crypto/ed25519"
	"testing"

	"example.org/domainreissue/q023"
)

func mustKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func mustBuild(t *testing.T, zone taskpkg.ZoneID, start, target taskpkg.Version,
	signer ed25519.PrivateKey, batches ...[]taskpkg.Operation,
) []taskpkg.Segment {
	t.Helper()
	builder, err := taskpkg.NewRoundBuilder(zone, start, target, signer)
	if err != nil {
		t.Fatalf("new builder: %v", err)
	}
	for _, batch := range batches {
		if err := builder.AddSegment(batch); err != nil {
			t.Fatalf("add segment: %v", err)
		}
	}
	segments, err := builder.Build()
	if err != nil {
		t.Fatalf("build round: %v", err)
	}
	return segments
}

func cloneSegments(segments []taskpkg.Segment) []taskpkg.Segment {
	clone := make([]taskpkg.Segment, len(segments))
	for i, seg := range segments {
		seg.Operations = append([]taskpkg.Operation(nil), seg.Operations...)
		for j, op := range seg.Operations {
			op.Value = append([]byte(nil), op.Value...)
			seg.Operations[j] = op
		}
		seg.Signature = append([]byte(nil), seg.Signature...)
		clone[i] = seg
	}
	return clone
}

func assertRejected(t *testing.T, store *taskpkg.Store, segments []taskpkg.Segment, want error) {
	t.Helper()
	err := store.Apply(segments)
	if err == nil {
		t.Fatalf("Apply unexpectedly accepted a malicious round")
	}
	if !errorsIs(err, want) {
		t.Fatalf("Apply error = %v, want error wrapping %v", err, want)
	}
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

func snapshot(store *taskpkg.Store, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if rec, ok := store.Lookup(key); ok {
			out[key] = string(rec.Value)
		} else {
			out[key] = "<absent>"
		}
	}
	return out
}

func mapsUnequal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return true
	}
	for key, value := range a {
		if b[key] != value {
			return true
		}
	}
	return false
}
