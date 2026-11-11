package pinnedtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"testing"
	"time"
)

const testHost = "example.test"

func ruleFor(leaf *testCert, intermediates ...*testCert) Rule {
	rule := Rule{
		Name:      testHost,
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
	}
	rule.PrimaryPins = append(rule.PrimaryPins, SPKISum(leaf.cert))
	for _, intermediate := range intermediates {
		rule.PrimaryPins = append(rule.PrimaryPins, SPKISum(intermediate.cert))
	}
	return rule
}

func chainCerts(entries ...any) []*x509.Certificate {
	chain := make([]*x509.Certificate, 0, len(entries))
	for _, entry := range entries {
		switch value := entry.(type) {
		case *testCert:
			chain = append(chain, value.cert)
		case *testCA:
			chain = append(chain, value.cert)
		default:
			panic("chainCerts: unsupported entry type")
		}
	}
	return chain
}

func verifyState(t *testing.T, verifier *Verifier, chain []*x509.Certificate, didResume bool) error {
	t.Helper()
	return verifier.VerifyConnection(tls.ConnectionState{
		ServerName:     testHost,
		VerifiedChains: [][]*x509.Certificate{chain},
		DidResume:      didResume,
	})
}

func TestRuleWindow(t *testing.T) {
	root := newRootCA("Root")
	intermediate := newIntermediate("Intermediate", root, nil)
	leaf := newLeaf(testHost, intermediate)
	pin := SPKISum(leaf.cert)

	start := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	rule := Rule{
		Name:        testHost,
		NotBefore:   start,
		NotAfter:    start.Add(time.Hour),
		PrimaryPins: []SPKIPin{pin},
	}

	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before window", start.Add(-time.Minute), false},
		{"at not before", start, true},
		{"inside window", start.Add(30 * time.Minute), true},
		{"at not after is exclusive", start.Add(time.Hour), false},
		{"after window", start.Add(time.Hour + time.Minute), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := fixedClock{t: tc.at}
			cfg, err := NewConfig([]Rule{rule})
			if err != nil {
				t.Fatalf("NewConfig: %v", err)
			}
			verifier, err := NewVerifier(cfg, WithClock(clock))
			if err != nil {
				t.Fatalf("NewVerifier: %v", err)
			}
			err = verifyState(t, verifier, chainCerts(leaf, intermediate, root), false)
			if tc.want && err != nil {
				t.Fatalf("expected pin to match at %s, got %v", tc.at, err)
			}
			if !tc.want {
				if err == nil {
					t.Fatalf("expected rule to be inactive at %s", tc.at)
				}
				if !errors.Is(err, ErrNoActiveRule) {
					t.Fatalf("expected ErrNoActiveRule, got %v", err)
				}
			}
		})
	}
}

func TestBackupPinAndPinMismatch(t *testing.T) {
	root := newRootCA("Root")
	intermediate := newIntermediate("Intermediate", root, nil)
	leaf := newLeaf(testHost, intermediate)
	backupLeaf := newLeaf(testHost, intermediate)

	rule := Rule{
		Name:        testHost,
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		PrimaryPins: []SPKIPin{SPKISum(leaf.cert)},
		BackupPins:  []SPKIPin{SPKISum(backupLeaf.cert)},
	}
	cfg, err := NewConfig([]Rule{rule})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	verifier, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := verifyState(t, verifier, chainCerts(leaf, intermediate, root), false); err != nil {
		t.Fatalf("primary pin should match: %v", err)
	}
	if err := verifyState(t, verifier, chainCerts(backupLeaf, intermediate, root), false); err != nil {
		t.Fatalf("backup pin should match: %v", err)
	}

	otherRoot := newRootCA("Other Root")
	otherIntermediate := newIntermediate("Other Intermediate", otherRoot, nil)
	otherLeaf := newLeaf(testHost, otherIntermediate)
	err = verifyState(t, verifier, chainCerts(otherLeaf, otherIntermediate, otherRoot), false)
	if err == nil {
		t.Fatal("unpinned chain must be rejected")
	}
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
	var pinErr *PinError
	if !errors.As(err, &pinErr) || pinErr.DidResume {
		t.Fatalf("expected full-handshake PinError, got %v", err)
	}
}

func TestCrossSignedChainMatchesSharedKey(t *testing.T) {
	rootA := newRootCA("Root A")
	rootB := newRootCA("Root B")

	// One intermediate key, two cross-certificates issued by distinct roots.
	intermediateKey := mustKey()
	intermediateA := newIntermediate("Intermediate", rootA, intermediateKey)
	intermediateB := newIntermediate("Intermediate", rootB, intermediateKey)

	leafA := newLeaf(testHost, intermediateA)
	leafB := newLeaf(testHost, intermediateB)

	// The pin is on the intermediate key. Both chains must satisfy it even
	// though the cross-certificates and roots differ.
	rule := Rule{
		Name:        testHost,
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		PrimaryPins: []SPKIPin{SPKISum(intermediateA.cert)},
	}
	cfg, err := NewConfig([]Rule{rule})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	verifier, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := verifyState(t, verifier, chainCerts(leafA, intermediateA, rootA), false); err != nil {
		t.Fatalf("chain under root A should match intermediate pin: %v", err)
	}
	if err := verifyState(t, verifier, chainCerts(leafB, intermediateB, rootB), true); err != nil {
		t.Fatalf("cross-signed chain under root B should match same key pin: %v", err)
	}
}

func TestAtomicConfigSwap(t *testing.T) {
	root := newRootCA("Root")
	intermediate := newIntermediate("Intermediate", root, nil)
	oldLeaf := newLeaf(testHost, intermediate)
	newLeaf := newLeaf(testHost, intermediate)

	oldRule := Rule{
		Name:        testHost + "-old",
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		PrimaryPins: []SPKIPin{SPKISum(oldLeaf.cert)},
	}
	newRule := Rule{
		Name:        testHost + "-new",
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		PrimaryPins: []SPKIPin{SPKISum(newLeaf.cert)},
	}

	oldCfg, err := NewConfig([]Rule{oldRule})
	if err != nil {
		t.Fatalf("NewConfig old: %v", err)
	}
	verifier, err := NewVerifier(oldCfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := verifyState(t, verifier, chainCerts(oldLeaf, intermediate, root), false); err != nil {
		t.Fatalf("old pin should match before rotation: %v", err)
	}

	newCfg, err := NewConfig([]Rule{newRule})
	if err != nil {
		t.Fatalf("NewConfig new: %v", err)
	}
	if err := verifier.Update(newCfg); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if verifier.Current() != newCfg {
		t.Fatal("Current() must return the swapped config atomically")
	}

	if err := verifyState(t, verifier, chainCerts(oldLeaf, intermediate, root), false); err == nil {
		t.Fatal("old key must fail after rotation")
	}
	if err := verifyState(t, verifier, chainCerts(newLeaf, intermediate, root), false); err != nil {
		t.Fatalf("new key must pass after rotation: %v", err)
	}

	if err := verifier.Update(nil); err == nil {
		t.Fatal("nil config update must be rejected")
	}
}

func TestConfigValidation(t *testing.T) {
	pin, err := DecodeSPKIPin(SPKIPin{}.String())
	if err != nil {
		t.Fatalf("DecodeSPKIPin: %v", err)
	}

	cases := []struct {
		name  string
		rules []Rule
	}{
		{"empty name", []Rule{{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), PrimaryPins: []SPKIPin{pin}}}},
		{"inverted window", []Rule{{Name: "x", NotBefore: time.Now(), NotAfter: time.Now().Add(-time.Hour), PrimaryPins: []SPKIPin{pin}}}},
		{"no pins", []Rule{{Name: "x", NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}}},
		{"duplicate backup", []Rule{{Name: "x", NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), PrimaryPins: []SPKIPin{pin}, BackupPins: []SPKIPin{pin}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewConfig(tc.rules); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	if _, err := NewConfig(nil); err != nil {
		t.Fatalf("empty rule set should be valid: %v", err)
	}
	if _, err := NewVerifier(nil); err == nil {
		t.Fatal("nil initial config must be rejected")
	}
}

func TestPinEncoding(t *testing.T) {
	root := newRootCA("Root")
	intermediate := newIntermediate("Intermediate", root, nil)
	leaf := newLeaf(testHost, intermediate)
	want := SPKISum(leaf.cert)

	decoded, err := DecodeSPKIPin(want.String())
	if err != nil {
		t.Fatalf("DecodeSPKIPin standard: %v", err)
	}
	if decoded != want {
		t.Fatal("standard base64 round trip mismatch")
	}

	raw, err := DecodeSPKIPin(base64Raw(want))
	if err != nil || raw != want {
		t.Fatalf("raw base64 round trip failed: %v", err)
	}
	if _, err := NewSPKIPin([]byte{1, 2, 3}); err == nil {
		t.Fatal("wrong-length digest must be rejected")
	}
}
