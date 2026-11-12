package netsec

import (
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func hashOf(payload string) string {
	sum := sha512.Sum512([]byte(payload))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// validLock returns a well-formed lock file: one direct package depending on
// one transitive package, plus a central-transitive entry referenced by the
// transitive one.
func validLock() (string, map[string]string) {
	hDirect := hashOf("direct nupkg")
	hTrans := hashOf("transitive nupkg")
	hCentral := hashOf("central nupkg")
	lock := fmt.Sprintf(`{
  "version": 1,
  "dependencies": {
    "net6.0": {
      "App.Direct": {
        "type": "Direct",
        "requested": "[1.0.0, 2.0.0)",
        "resolved": "1.5.0",
        "contentHash": %q,
        "dependencies": { "Lib.Trans": "1.2.0" }
      },
      "Lib.Trans": {
        "type": "Transitive",
        "resolved": "1.2.0",
        "contentHash": %q,
        "dependencies": { "Shared.Central": "[1.0.0, )" }
      },
      "Shared.Central": {
        "type": "CentralTransitive",
        "resolved": "1.0.4",
        "contentHash": %q
      }
    }
  }
}`, hDirect, hTrans, hCentral)
	actual := map[string]string{
		ActualHashKey("App.Direct", "1.5.0"):     hDirect,
		ActualHashKey("Lib.Trans", "1.2.0"):      hTrans,
		ActualHashKey("Shared.Central", "1.0.4"): hCentral,
	}
	return lock, actual
}

func verify(t *testing.T, lock string, actual map[string]string) *Error {
	t.Helper()
	lf, err := Parse(strings.NewReader(lock))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	err = lf.Verify(actual)
	if err == nil {
		return nil
	}
	var verr *Error
	if !errors.As(err, &verr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	return verr
}

func TestVerifyValidLock(t *testing.T) {
	lock, actual := validLock()
	if verr := verify(t, lock, actual); verr != nil {
		t.Fatalf("valid lock rejected: %v", verr)
	}
}

func TestDirectResolvedOutsideRequestedRange(t *testing.T) {
	lock, actual := validLock()
	lock = strings.Replace(lock, `"resolved": "1.5.0"`, `"resolved": "2.0.0"`, 1)
	delete(actual, ActualHashKey("App.Direct", "1.5.0"))
	actual[ActualHashKey("App.Direct", "2.0.0")] = hashOf("direct nupkg")

	verr := verify(t, lock, actual)
	if verr == nil || verr.Kind != ErrKindRangeViolation {
		t.Fatalf("expected range violation, got %v", verr)
	}
}

func TestContentHashMismatch(t *testing.T) {
	lock, actual := validLock()
	actual[ActualHashKey("Lib.Trans", "1.2.0")] = hashOf("tampered nupkg")

	verr := verify(t, lock, actual)
	if verr == nil || verr.Kind != ErrKindHashMismatch {
		t.Fatalf("expected hash mismatch, got %v", verr)
	}
}

func TestCaseInsensitiveDuplicateResolved(t *testing.T) {
	lock, actual := validLock()
	// Same package ID with different casing and a different resolved version.
	lock = strings.Replace(lock, `"Lib.Trans": {`, `"lib.trans": {
        "type": "Transitive",
        "resolved": "1.3.0",
        "contentHash": `+fmt.Sprintf("%q", hashOf("transitive nupkg"))+`
      },
      "Lib.Trans": {`, 1)

	verr := verify(t, lock, actual)
	if verr == nil || verr.Kind != ErrKindDuplicateResolved {
		t.Fatalf("expected duplicate resolved rejection, got %v", verr)
	}
}

func TestOrphanTransitiveRejected(t *testing.T) {
	lock, actual := validLock()
	// Inject a transitive package nobody references.
	lock = strings.Replace(lock, `"Shared.Central": {`, `"Evil.Injected": {
        "type": "Transitive",
        "resolved": "9.9.9",
        "contentHash": `+fmt.Sprintf("%q", hashOf("evil nupkg"))+`
      },
      "Shared.Central": {`, 1)
	actual[ActualHashKey("Evil.Injected", "9.9.9")] = hashOf("evil nupkg")

	verr := verify(t, lock, actual)
	if verr == nil || verr.Kind != ErrKindOrphanTransitive {
		t.Fatalf("expected orphan transitive rejection, got %v", verr)
	}
}

func TestMissingContentHashRejected(t *testing.T) {
	lock, actual := validLock()
	lock = strings.Replace(lock, fmt.Sprintf(`"contentHash": %q,`, hashOf("transitive nupkg")), "", 1)

	verr := verify(t, lock, actual)
	if verr == nil || verr.Kind != ErrKindMissingHash {
		t.Fatalf("expected missing hash rejection, got %v", verr)
	}
}

func TestVersionRangeSatisfaction(t *testing.T) {
	cases := []struct {
		rng, ver string
		want     bool
	}{
		{"[1.0, 2.0)", "1.0", true},
		{"[1.0, 2.0)", "1.9.9", true},
		{"[1.0, 2.0)", "2.0", false},
		{"[1.0, 2.0)", "0.9", false},
		{"(1.0, 2.0]", "1.0", false},
		{"(1.0, 2.0]", "2.0", true},
		{"[1.2.3]", "1.2.3", true},
		{"[1.2.3]", "1.2.4", false},
		{"1.0", "1.0", true},
		{"1.0", "5.0", true},
		{"1.0", "0.9", false},
		{"(, 2.0]", "1.5", true},
		{"[2.0, )", "2.0.0-rc.1", false},
		{"[2.0.0-rc.1, )", "2.0.0-rc.1", true},
		{"[1.0, 2.0)", "1.0.0-alpha", false},
	}
	for _, c := range cases {
		r, err := ParseVersionRange(c.rng)
		if err != nil {
			t.Fatalf("ParseVersionRange(%q): %v", c.rng, err)
		}
		v, err := ParseVersion(c.ver)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.ver, err)
		}
		if got := r.SatisfiedBy(v); got != c.want {
			t.Errorf("range %q satisfied by %q: got %v, want %v", c.rng, c.ver, got, c.want)
		}
	}
}
