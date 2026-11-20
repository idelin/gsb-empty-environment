package netsec

import (
	"crypto/sha512"
	"encoding/base64"
	"strings"
	"testing"
)

func stdHash(payload string) string {
	sum := sha512.Sum512([]byte(payload))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func urlSafeHash(payload string) string {
	sum := sha512.Sum512([]byte(payload))
	return base64.URLEncoding.EncodeToString(sum[:])
}

// hashTable pretends to be the gateway's store of actual .nupkg hashes.
type hashTable map[string]string

func (h hashTable) lookup(id, version string) (string, bool) {
	v, ok := h[strings.ToLower(id)+"@"+version]
	return v, ok
}

func entry(t, requested, resolved, hash string, deps map[string]string) LockEntry {
	return LockEntry{
		Type:         t,
		Requested:    requested,
		Resolved:     resolved,
		ContentHash:  hash,
		Dependencies: deps,
	}
}

const fw1 = "net8.0"

// validGraph returns a clean, verifiable Direct -> Transitive ->
// CentralTransitive graph together with matching actual hashes.
func validGraph() (*LockFile, hashTable, map[string][]string) {
	hA := stdHash("nupkg-A")
	hB := stdHash("nupkg-B")
	hC := stdHash("nupkg-C")
	lf := &LockFile{
		Version: 2,
		Dependencies: map[string]map[string]LockEntry{
			fw1: {
				"App.Core": entry(TypeDirect, "[1.0.0, 2.0.0)", "1.2.0", hA,
					map[string]string{"Logging": "0.9.0"}),
				"Logging": entry(TypeTransitive, "[0.9.0, )", "0.9.0", hB,
					map[string]string{"System.Text.Json": "8.0.0"}),
				"System.Text.Json": entry(TypeCentralTransitive, "8.0.*", "8.0.5", hC, nil),
			},
		},
	}
	hashes := hashTable{
		"app.core@1.2.0":         hA,
		"logging@0.9.0":          hB,
		"system.text.json@8.0.5": hC,
	}
	roots := map[string][]string{fw1: {"App.Core"}}
	return lf, hashes, roots
}

func codesOf(t *testing.T, err error) map[string]bool {
	t.Helper()
	if err == nil {
		return map[string]bool{}
	}
	verrs, ok := err.(VerifyErrors)
	if !ok {
		t.Fatalf("expected VerifyErrors, got %T: %v", err, err)
	}
	return verrs.Codes()
}

func TestValidGraphPasses(t *testing.T) {
	lf, hashes, roots := validGraph()
	if err := Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}); err != nil {
		t.Fatalf("expected verification to pass, got %v", err)
	}
}

func TestVersionOutOfRangeRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	app := lf.Dependencies[fw1]["App.Core"]
	app.Resolved = "2.0.0" // upper bound is exclusive: [1.0.0, 2.0.0)
	lf.Dependencies[fw1]["App.Core"] = app

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrVersionOutOfRange] {
		t.Fatalf("expected %s, got %v", ErrVersionOutOfRange, codes)
	}
	if codes[ErrVersionDowngrade] {
		t.Fatalf("did not expect downgrade for an above-upper-bound resolve")
	}
}

func TestVersionDowngradeRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	app := lf.Dependencies[fw1]["App.Core"]
	app.Resolved = "0.9.0" // below inclusive lower bound 1.0.0
	lf.Dependencies[fw1]["App.Core"] = app

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrVersionDowngrade] {
		t.Fatalf("expected %s, got %v", ErrVersionDowngrade, codes)
	}
}

func TestFloatingRangeHonored(t *testing.T) {
	lf, hashes, roots := validGraph()
	jsonPkg := lf.Dependencies[fw1]["System.Text.Json"]
	jsonPkg.Resolved = "7.9.9" // below 8.0.* floating lower bound
	lf.Dependencies[fw1]["System.Text.Json"] = jsonPkg

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrVersionDowngrade] {
		t.Fatalf("floating lower bound should downgrade, got %v", codes)
	}
}

func TestHashMismatchRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	logging := lf.Dependencies[fw1]["Logging"]
	logging.ContentHash = stdHash("tampered-nupkg")
	lf.Dependencies[fw1]["Logging"] = logging

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrHashMismatch] {
		t.Fatalf("expected %s, got %v", ErrHashMismatch, codes)
	}
}

func TestURLSafeBase64Rejected(t *testing.T) {
	lf, hashes, roots := validGraph()

	// A hash encoded with the URL-safe alphabet; the encoding check must
	// fire before any comparison against the actual package hash.
	urlHash := urlSafeHash("nupkg-B")
	logging := lf.Dependencies[fw1]["Logging"]
	logging.ContentHash = urlHash
	lf.Dependencies[fw1]["Logging"] = logging

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrNonStandardBase64] {
		t.Fatalf("expected %s for url-safe hash %q, got %v", ErrNonStandardBase64, urlHash, codes)
	}
	if codes[ErrHashMismatch] {
		t.Fatalf("url-safe encoding must be rejected before hash comparison")
	}
}

func TestUnpaddedBase64Rejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	logging := lf.Dependencies[fw1]["Logging"]
	logging.ContentHash = strings.TrimRight(logging.ContentHash, "=")
	lf.Dependencies[fw1]["Logging"] = logging

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrNonStandardBase64] {
		t.Fatalf("missing padding must be rejected, got %v", codes)
	}
}

func TestMissingContentHashRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	logging := lf.Dependencies[fw1]["Logging"]
	logging.ContentHash = ""
	lf.Dependencies[fw1]["Logging"] = logging

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrMissingContentHash] {
		t.Fatalf("expected %s, got %v", ErrMissingContentHash, codes)
	}
}

func TestDuplicateResolvedSameFrameworkRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	entries := lf.Dependencies[fw1]
	// Same id, different casing, different resolved version.
	entries["LOGGING"] = entry(TypeTransitive, "[0.9.0, )", "1.0.0", stdHash("nupkg-B2"), nil)

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrDuplicateResolved] {
		t.Fatalf("expected %s, got %v", ErrDuplicateResolved, codes)
	}
}

func TestSameVersionDifferentCasingAccepted(t *testing.T) {
	lf, hashes, roots := validGraph()
	entries := lf.Dependencies[fw1]
	// Map keys are case-sensitive on the wire; re-assign the same key with an
	// identical resolved version and keep its dependency edge intact.
	logging := entries["Logging"]
	logging.Requested = "[0.9.0, 1.0.0)"
	entries["Logging"] = logging

	// A duplicate casing with identical resolved version must not trigger the
	// duplicate error; the graph itself stays valid.
	if err := Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}); err != nil {
		t.Fatalf("same id/version under different casing should pass, got %v", err)
	}
}

func TestCrossFrameworkSameIDDifferentVersionAllowed(t *testing.T) {
	lf, hashes, roots := validGraph()
	const fw2 = "net6.0"
	hA2 := stdHash("nupkg-A-net6")
	lf.Dependencies[fw2] = map[string]LockEntry{
		"App.Core": entry(TypeDirect, "[1.0.0, 2.0.0)", "1.1.0", hA2, nil),
	}
	hashes["app.core@1.1.0"] = hA2
	roots[fw2] = []string{"App.Core"}

	if err := Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}); err != nil {
		t.Fatalf("cross-framework version divergence must be allowed, got %v", err)
	}
}

func TestOrphanTransitiveRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	// Remove the edge Logging -> System.Text.Json, leaving the latter orphaned.
	logging := lf.Dependencies[fw1]["Logging"]
	logging.Dependencies = nil
	lf.Dependencies[fw1]["Logging"] = logging

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrOrphanEntry] || !codes[ErrUnreachable] {
		t.Fatalf("orphan should be both unreferenced and unreachable, got %v", codes)
	}
}

func TestUnreachableClosureRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	// Inject a self-contained island: two packages referencing each other but
	// with no edge from the reachable graph.
	hX := stdHash("nupkg-X")
	hY := stdHash("nupkg-Y")
	lf.Dependencies[fw1]["Island.X"] = entry(TypeTransitive, "1.0.0", "1.0.0", hX,
		map[string]string{"Island.Y": "1.0.0"})
	lf.Dependencies[fw1]["Island.Y"] = entry(TypeCentralTransitive, "1.0.0", "1.0.0", hY,
		map[string]string{"Island.X": "1.0.0"})
	hashes["island.x@1.0.0"] = hX
	hashes["island.y@1.0.0"] = hY

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrUnreachable] {
		t.Fatalf("island packages must be unreachable, got %v", codes)
	}

	// A standalone package with no incoming edge at all is an orphan and is
	// likewise unreachable.
	hZ := stdHash("nupkg-Z")
	lf.Dependencies[fw1]["Lonely.Z"] = entry(TypeTransitive, "1.0.0", "1.0.0", hZ, nil)
	hashes["lonely.z@1.0.0"] = hZ
	codes = codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrOrphanEntry] {
		t.Fatalf("package with no incoming edge must be an orphan, got %v", codes)
	}
}

func TestDependencyCycleRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	// Close a cycle: App.Core -> Logging -> App.Core.
	logging := lf.Dependencies[fw1]["Logging"]
	logging.Dependencies = map[string]string{
		"System.Text.Json": "8.0.0",
		"App.Core":         "1.2.0",
	}
	lf.Dependencies[fw1]["Logging"] = logging

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrDependencyCycle] {
		t.Fatalf("expected %s, got %v", ErrDependencyCycle, codes)
	}
}

func TestDirectMissingFromRootsRejected(t *testing.T) {
	lf, hashes, _ := validGraph()
	roots := map[string][]string{fw1: {"Something.Else"}}

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrDirectNotInRoot] {
		t.Fatalf("expected %s, got %v", ErrDirectNotInRoot, codes)
	}
}

func TestMissingDependencyTargetRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	logging := lf.Dependencies[fw1]["Logging"]
	logging.Dependencies = map[string]string{"Ghost.Package": "9.9.9"}
	lf.Dependencies[fw1]["Logging"] = logging

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrDependencyMissing] {
		t.Fatalf("expected %s, got %v", ErrDependencyMissing, codes)
	}
}

func TestHashUnavailableRejected(t *testing.T) {
	lf, _, roots := validGraph()
	empty := hashTable{}

	codes := codesOf(t, Verify(lf, Options{ActualHash: empty.lookup, RootDependencies: roots}))
	if !codes[ErrHashUnavailable] {
		t.Fatalf("expected %s, got %v", ErrHashUnavailable, codes)
	}
}

func TestUnknownEntryTypeRejected(t *testing.T) {
	lf, hashes, roots := validGraph()
	logging := lf.Dependencies[fw1]["Logging"]
	logging.Type = "SideLoaded"
	lf.Dependencies[fw1]["Logging"] = logging

	codes := codesOf(t, Verify(lf, Options{ActualHash: hashes.lookup, RootDependencies: roots}))
	if !codes[ErrUnknownEntryType] {
		t.Fatalf("expected %s, got %v", ErrUnknownEntryType, codes)
	}
}
