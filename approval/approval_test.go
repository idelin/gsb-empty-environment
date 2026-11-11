package approval

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"
)

type keyMaterial struct {
	alias string
	role  Role
	alg   Algorithm
	priv  any
	pub   any
}

func generateKeys(t *testing.T) []keyMaterial {
	t.Helper()

	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	ec256Priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate p-256: %v", err)
	}
	ec384Priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate p-384: %v", err)
	}
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}

	return []keyMaterial{
		{alias: "admin-ed", role: RoleAdmin, alg: AlgEd25519, priv: edPriv, pub: edPub},
		{alias: "admin-ec", role: RoleAdmin, alg: AlgES256, priv: ec256Priv, pub: &ec256Priv.PublicKey},
		{alias: "device-ec", role: RoleDevice, alg: AlgES384, priv: ec384Priv, pub: &ec384Priv.PublicKey},
		{alias: "device-rsa", role: RoleDevice, alg: AlgPS256, priv: rsaPriv, pub: &rsaPriv.PublicKey},
	}
}

func trustedEntries(keys []keyMaterial) []TrustedKey {
	entries := make([]TrustedKey, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, TrustedKey{
			Alias:     key.alias,
			Role:      key.role,
			Algorithm: key.alg,
			PublicKey: key.pub,
		})
	}
	return entries
}

func findKey(keys []keyMaterial, alias string) keyMaterial {
	for _, key := range keys {
		if key.alias == alias {
			return key
		}
	}
	panic("unknown test key alias: " + alias)
}

func baseRequest(deadline time.Time) UpdateRequest {
	return UpdateRequest{
		Version:          "1",
		DeviceGroup:      "fleet-canary",
		OperationSummary: "apply firmware 2.4.1",
		Deadline:         deadline,
	}
}

func signFor(t *testing.T, key keyMaterial, req UpdateRequest) Signature {
	t.Helper()
	value, err := Sign(key.priv, key.alg, req)
	if err != nil {
		t.Fatalf("sign with %s: %v", key.alias, err)
	}
	return Signature{Alias: key.alias, Algorithm: key.alg, Value: value}
}

func policy() Thresholds {
	return Thresholds{Admin: 2, Device: 2}
}

// TestVerifyAcceptsDistinctKeys covers the happy path: four distinct key
// materials across the two roles meet both thresholds with real signatures.
func TestVerifyAcceptsDistinctKeys(t *testing.T) {
	keys := generateKeys(t)
	ks, err := NewKeySet(trustedEntries(keys))
	if err != nil {
		t.Fatalf("new key set: %v", err)
	}

	req := baseRequest(time.Now().Add(time.Hour))
	for _, key := range keys {
		req.Signatures = append(req.Signatures, signFor(t, key, req))
	}

	result, err := Verify(time.Now(), req, ks, policy())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Admin != 2 || result.Device != 2 {
		t.Fatalf("unexpected tally: %+v", result)
	}
}

// TestVerifyRoleMixingCannotPromoteVote makes sure a device key that claims
// the admin role still votes only as a device, so the admin threshold fails.
func TestVerifyRoleMixingCannotPromoteVote(t *testing.T) {
	keys := generateKeys(t)
	ks, err := NewKeySet(trustedEntries(keys))
	if err != nil {
		t.Fatalf("new key set: %v", err)
	}

	req := baseRequest(time.Now().Add(time.Hour))
	adminOne := findKey(keys, "admin-ed")
	deviceOne := findKey(keys, "device-ec")
	deviceTwo := findKey(keys, "device-rsa")

	req.Signatures = append(req.Signatures, signFor(t, adminOne, req))
	cheating := signFor(t, deviceOne, req)
	cheating.ClaimedRole = RoleAdmin // must be ignored
	req.Signatures = append(req.Signatures, cheating)
	req.Signatures = append(req.Signatures, signFor(t, deviceTwo, req))

	result, err := Verify(time.Now(), req, ks, policy())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if result.Admin != 1 || result.Device != 2 {
		t.Fatalf("role mixing miscounted: %+v", result)
	}
}

// TestVerifyDuplicateAliasesAndRepeatedSignatures ensures repeated signatures
// and the same key material registered under multiple aliases never add votes.
func TestVerifyDuplicateAliasesAndRepeatedSignatures(t *testing.T) {
	keys := generateKeys(t)

	// Register the admin-ed key material again under a second alias. The same
	// role is permitted, but it must not gain a second vote.
	original := findKey(keys, "admin-ed")
	extra := keyMaterial{
		alias: "admin-ed-alias",
		role:  RoleAdmin,
		alg:   original.alg,
		priv:  original.priv,
		pub:   original.pub,
	}
	entries := append(trustedEntries(keys), TrustedKey{
		Alias:     extra.alias,
		Role:      extra.role,
		Algorithm: extra.alg,
		PublicKey: extra.pub,
	})
	ks, err := NewKeySet(entries)
	if err != nil {
		t.Fatalf("new key set with alias: %v", err)
	}

	req := baseRequest(time.Now().Add(time.Hour))

	// Same key, same alias, submitted twice verbatim.
	first := signFor(t, original, req)
	req.Signatures = append(req.Signatures, first, first)

	// Same key material again, presented through the second alias.
	req.Signatures = append(req.Signatures, signFor(t, extra, req))

	for _, key := range keys[1:] {
		req.Signatures = append(req.Signatures, signFor(t, key, req))
	}

	result, err := Verify(time.Now(), req, ks, policy())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Admin != 2 || result.Device != 2 {
		t.Fatalf("duplicate material inflated tally: %+v", result)
	}

	// Without the second distinct admin key the threshold must fail even with
	// three admin-looking signature entries.
	var reduced []TrustedKey
	for _, entry := range entries {
		if entry.Alias != "admin-ec" {
			reduced = append(reduced, entry)
		}
	}
	ksReduced, err := NewKeySet(reduced)
	if err != nil {
		t.Fatalf("reduced key set: %v", err)
	}

	// Build a request whose aliases all exist in the reduced set: only one
	// distinct admin key material remains, even though it signs three times.
	reducedReq := baseRequest(req.Deadline)
	reducedReq.Signatures = append(reducedReq.Signatures, first, first)
	reducedReq.Signatures = append(reducedReq.Signatures, signFor(t, extra, reducedReq))
	for _, key := range keys[2:] {
		reducedReq.Signatures = append(reducedReq.Signatures, signFor(t, key, reducedReq))
	}

	_, err = Verify(time.Now(), reducedReq, ksReduced, policy())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for single admin material, got %v", err)
	}
}

// TestVerifyRejectsDuplicateAliasRegistration confirms NewKeySet refuses to
// register the same alias twice.
func TestVerifyRejectsDuplicateAliasRegistration(t *testing.T) {
	keys := generateKeys(t)
	entries := trustedEntries(keys)
	entries = append(entries, TrustedKey{
		Alias:     "admin-ed",
		Role:      RoleAdmin,
		Algorithm: AlgEd25519,
		PublicKey: findKey(keys, "admin-ed").pub,
	})
	if _, err := NewKeySet(entries); err == nil {
		t.Fatal("expected duplicate alias to be rejected")
	}
}

// TestVerifyContentConcatenationCannotCollide confirms length-prefixed framing
// prevents cross-field confusion and that any covered-field change breaks the
// signature.
func TestVerifyContentConcatenationCannotCollide(t *testing.T) {
	keys := generateKeys(t)
	ks, err := NewKeySet(trustedEntries(keys))
	if err != nil {
		t.Fatalf("new key set: %v", err)
	}

	deadline := time.Now().Add(time.Hour)
	req := baseRequest(deadline)
	for _, key := range keys {
		req.Signatures = append(req.Signatures, signFor(t, key, req))
	}

	// A naive concatenation of ("ab","c") equals ("a","bc"); framing must not.
	framedA := SigningBytes(UpdateRequest{
		Version:          "ab",
		DeviceGroup:      "c",
		OperationSummary: "x",
		Deadline:         deadline,
	})
	framedB := SigningBytes(UpdateRequest{
		Version:          "a",
		DeviceGroup:      "bc",
		OperationSummary: "x",
		Deadline:         deadline,
	})
	if string(framedA) == string(framedB) {
		t.Fatal("length-prefixed framing produced colliding payloads")
	}

	mutations := map[string]func(*UpdateRequest){
		"version":  func(r *UpdateRequest) { r.Version = "2" },
		"group":    func(r *UpdateRequest) { r.DeviceGroup = "fleet-stable" },
		"summary":  func(r *UpdateRequest) { r.OperationSummary = "apply firmware 9.9.9" },
		"deadline": func(r *UpdateRequest) { r.Deadline = r.Deadline.Add(time.Minute) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := req
			mutate(&changed)
			if _, err := Verify(time.Now(), changed, ks, policy()); !errors.Is(err, ErrBadSignature) {
				t.Fatalf("expected ErrBadSignature when %s changes, got %v", name, err)
			}
		})
	}
}

// TestVerifyDeadlineBoundary pins the expiry rule: the deadline instant itself
// is accepted, one nanosecond later is expired.
func TestVerifyDeadlineBoundary(t *testing.T) {
	keys := generateKeys(t)
	ks, err := NewKeySet(trustedEntries(keys))
	if err != nil {
		t.Fatalf("new key set: %v", err)
	}

	deadline := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	req := baseRequest(deadline)
	for _, key := range keys {
		req.Signatures = append(req.Signatures, signFor(t, key, req))
	}

	if _, err := Verify(deadline, req, ks, policy()); err != nil {
		t.Fatalf("request at deadline should be accepted: %v", err)
	}
	if _, err := Verify(deadline.Add(time.Nanosecond), req, ks, policy()); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired after deadline, got %v", err)
	}
	if _, err := Verify(deadline.Add(-time.Nanosecond), req, ks, policy()); err != nil {
		t.Fatalf("request just before deadline should be accepted: %v", err)
	}
}

// TestVerifyRejectsUnknownAlgorithmAndKeyProblems checks the failure paths for
// unknown algorithms, unknown aliases and tampered signature bytes.
func TestVerifyRejectsUnknownAlgorithmAndKeyProblems(t *testing.T) {
	keys := generateKeys(t)
	ks, err := NewKeySet(trustedEntries(keys))
	if err != nil {
		t.Fatalf("new key set: %v", err)
	}

	req := baseRequest(time.Now().Add(time.Hour))
	signed := signFor(t, findKey(keys, "admin-ed"), req)

	unknown := req
	unknown.Signatures = []Signature{{
		Alias:     "admin-ed",
		Algorithm: "MADE-UP-ALG",
		Value:     signed.Value,
	}}
	if _, err := Verify(time.Now(), unknown, ks, Thresholds{Admin: 1}); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("expected ErrUnknownAlgorithm, got %v", err)
	}

	// The algorithm must match the one registered for the key.
	mismatched := req
	mismatched.Signatures = []Signature{{
		Alias:     "admin-ed",
		Algorithm: AlgES256,
		Value:     signed.Value,
	}}
	if _, err := Verify(time.Now(), mismatched, ks, Thresholds{Admin: 1}); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("expected ErrUnknownAlgorithm for alg mismatch, got %v", err)
	}

	missingAlias := req
	missingAlias.Signatures = []Signature{{
		Alias:     "ghost",
		Algorithm: AlgEd25519,
		Value:     signed.Value,
	}}
	if _, err := Verify(time.Now(), missingAlias, ks, Thresholds{Admin: 1}); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("expected ErrUnknownKey, got %v", err)
	}

	tampered := req
	tamperedValue := append([]byte(nil), signed.Value...)
	tamperedValue[0] ^= 0xFF
	tampered.Signatures = []Signature{{
		Alias:     signed.Alias,
		Algorithm: signed.Algorithm,
		Value:     tamperedValue,
	}}
	if _, err := Verify(time.Now(), tampered, ks, Thresholds{Admin: 1}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
}

// TestNewKeySetRejectsConflictingRolesForSameMaterial ensures the same key
// cannot be declared both an admin and a device key via different aliases.
func TestNewKeySetRejectsConflictingRolesForSameMaterial(t *testing.T) {
	keys := generateKeys(t)
	adminED := findKey(keys, "admin-ed")

	entries := trustedEntries(keys)
	entries = append(entries, TrustedKey{
		Alias:     "admin-ed-as-device",
		Role:      RoleDevice,
		Algorithm: AlgEd25519,
		PublicKey: adminED.pub,
	})
	if _, err := NewKeySet(entries); err == nil {
		t.Fatal("expected conflicting role registration to be rejected")
	}
}
