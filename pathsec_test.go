package pathsec

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"testing"
	"time"
)

func TestROAValidationAndExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	roas := NewROATable().SetClock(func() time.Time { return now })
	if err := roas.Add(ROA{Prefix: "10.0.0.0/16", MaxLength: 24, OriginASN: 65001, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	if got := roas.Validate("10.0.1.0/24", 65001); got.Status != StatusValid {
		t.Fatalf("expected valid ROA, got %s: %+v", got.Status, got.Reasons)
	}
	if got := roas.Validate("10.0.0.0/25", 65001); got.Status != StatusInvalid {
		t.Fatalf("expected maxLength invalid, got %s", got.Status)
	}
	if got := roas.Validate("10.0.1.0/24", 65002); got.Status != StatusInvalid {
		t.Fatalf("expected origin invalid, got %s", got.Status)
	}
	if got := roas.Validate("192.168.0.0/24", 65001); got.Status != StatusNotFound {
		t.Fatalf("expected NotFound, got %s", got.Status)
	}

	later := now.Add(2 * time.Hour)
	if got := roas.Validate("10.0.1.0/24", 65001, later); got.Status != StatusNotFound {
		t.Fatalf("expected expired ROA to be NotFound, got %s", got.Status)
	}
}

func TestASPAExplicitEmptyVersusMissing(t *testing.T) {
	table := NewASPATable()
	if err := table.PublishEmpty(65010); err != nil {
		t.Fatal(err)
	}
	if table.Check(65010, 65020) != StatusInvalid {
		t.Fatal("explicit empty ASPA must explicitly reject providers")
	}
	if table.Check(65030, 65020) != StatusUnknown {
		t.Fatal("unpublished ASPA must be Unknown")
	}
	raw, ok := table.RawProviders(65010)
	if !ok || len(raw) != 1 || raw[0] != AS0 {
		t.Fatalf("expected raw AS0 marker, got %v ok=%v", raw, ok)
	}

	path := []uint32{65020, 65010}
	invalid := VerifyASPA(table, ASPARequest{ASPath: path, Role: RoleCustomer})
	if invalid.Status != StatusInvalid {
		t.Fatalf("explicit empty downstream customer should be Invalid, got %s", invalid.Status)
	}
	unknown := VerifyASPA(NewASPATable(), ASPARequest{ASPath: path, Role: RoleCustomer})
	if unknown.Status != StatusUnknown {
		t.Fatalf("missing ASPA should be Unknown, got %s", unknown.Status)
	}
}

func TestASPACustomerValleyAndProviderTwoRamps(t *testing.T) {
	table := NewASPATable()
	table.MustPublish(100, 400)
	table.MustPublish(200, 100, 300)
	table.MustPublish(400)

	// Customer-facing path [provider-of-origin, origin-customer] in
	// conventional order; ASPA 100 -> 400 means AS100 is customer of AS400,
	// so the valid customer-cone sequence is [400, 100].
	customerPath := []uint32{400, 100}
	if got := VerifyASPA(table, ASPARequest{ASPath: customerPath, Role: RoleCustomer}); got.Status != StatusValid {
		t.Fatalf("customer cone should be Valid, got %s %+v", got.Status, got.Reasons)
	}

	// [200,100,400] has receiver ramp 200->100 and origin ramp 400? Wait
	// 400 is explicit empty, so use a valid overlap path [200,100] with
	// 100 provider of 200.
	if got := VerifyASPA(table, ASPARequest{ASPath: []uint32{200, 100}, Role: RoleProvider}); got.Status != StatusValid {
		t.Fatalf("provider path should be Valid, got %s", got.Status)
	}

	// The leak path looks two-ramp valid without the customer role, but the
	// customer perspective must reject the D->U valley.
	leakTable := NewASPATable()
	leakTable.MustPublish(1, 2)
	leakTable.MustPublish(3, 2)
	leakTable.MustPublish(2)
	leak := []uint32{1, 2, 3}
	generic := VerifyASPA(leakTable, ASPARequest{ASPath: leak, Role: RoleProvider})
	if generic.Status != StatusValid {
		t.Fatalf("generic two-ramp path should be Valid, got %s", generic.Status)
	}
	customer := VerifyASPA(leakTable, ASPARequest{ASPath: leak, Role: RoleCustomer})
	if customer.Status != StatusInvalid {
		t.Fatalf("customer-side valley should be Invalid, got %s", customer.Status)
	}
	weak := EvaluateWeakASPA(leakTable, ASPARequest{ASPath: leak, Role: RoleCustomer}, WeakASPAOneSidedEither)
	if weak != StatusValid {
		t.Fatalf("weak one-sided checker should miss valley, got %s", weak)
	}
}

func TestNormalizeASPathRejectsSetAndLoop(t *testing.T) {
	np := NormalizeASNs([]uint32{1, 1, 2, 2, 3})
	if len(np.ASNs) != 3 || np.ASNs[0] != 1 || np.Runs[0] != 2 {
		t.Fatalf("adjacent dedup failed: %+v", np)
	}
	loop := VerifyASPA(nil, ASPARequest{ASPath: []uint32{1, 2, 1}, Role: RolePeer})
	if loop.Status != StatusInvalid {
		t.Fatalf("non-adjacent loop must be Invalid, got %s", loop.Status)
	}
	set := VerifyASPA(nil, ASPARequest{Segments: []ASPathSegment{{Type: SegmentSet, ASNs: []uint32{1, 2}}}})
	if set.Status != StatusInvalid {
		t.Fatalf("AS_SET must be rejected, got %s", set.Status)
	}
}

func TestBGPsecEd25519ChainAndTampering(t *testing.T) {
	path := []uint32{100, 200, 300}
	localAS := uint32(9)
	pub1, priv1, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	pub2, priv2, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	pub3, priv3, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	keys := NewKeyStore().
		TrustEd25519(100, pub1).
		TrustEd25519(200, pub2).
		TrustEd25519(300, pub3)
	signers := map[uint32]*Signer{
		100: NewEd25519Signer(100, priv1),
		200: NewEd25519Signer(200, priv2),
		300: NewEd25519Signer(300, priv3),
	}
	proof, err := SignBGPsecChain("10.1.0.0/16", path, localAS, signers, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := BGPsecRequest{Prefix: "10.1.0.0/16", ASPath: path, Proof: proof, LocalASN: localAS}
	if got := VerifyBGPsec(keys, req); got.Status != StatusValid {
		t.Fatalf("expected valid Ed25519 chain: %+v", got.Reasons)
	}

	tamperedPrefix := req
	tamperedPrefix.Prefix = "10.2.0.0/16"
	if got := VerifyBGPsec(keys, tamperedPrefix); got.Status != StatusInvalid {
		t.Fatalf("prefix tamper must invalidate, got %s", got.Status)
	}

	zeroPCount := *proof
	zeroPCount.SecurePath = append([]SecurePathHop(nil), proof.SecurePath...)
	zeroPCount.SecurePath[1].PCount = 0
	zeroReq := req
	zeroReq.Proof = &zeroPCount
	if got := VerifyBGPsec(keys, zeroReq); got.Status != StatusInvalid {
		t.Fatalf("pCount=0 must invalidate, got %s", got.Status)
	}
	zeroReq.IgnorePCount = true
	// The zero value is still signed by the original signer only if re-signed;
	// here signature/payload mismatch demonstrates cryptographic binding too.
	if got := VerifyBGPsec(keys, zeroReq); got.Status == StatusValid {
		t.Fatal("mutated pCount without re-signature must not verify even when pCount is ignored")
	}

	truncated := *proof
	truncated.SecurePath = proof.SecurePath[:2]
	truncated.SignatureBlock = proof.SignatureBlock[:2]
	truncReq := req
	truncReq.Proof = &truncated
	if got := VerifyBGPsec(keys, truncReq); got.Status != StatusInvalid {
		t.Fatalf("truncated tail must invalidate, got %s", got.Status)
	}
}

func TestBGPsecECDSAP256RealVerification(t *testing.T) {
	path := []uint32{7, 8}
	local := uint32(6)
	key1, err := GenerateECDSAP256Key()
	if err != nil {
		t.Fatal(err)
	}
	key2, err := GenerateECDSAP256Key()
	if err != nil {
		t.Fatal(err)
	}
	keys := NewKeyStore().TrustECDSAP256(7, &key1.PublicKey).TrustECDSAP256(8, &key2.PublicKey)
	signers := map[uint32]*Signer{
		7: NewECDSAP256Signer(7, key1),
		8: NewECDSAP256Signer(8, key2),
	}
	proof, err := SignBGPsecChain("2001:db8::/32", path, local, signers, []int{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := VerifyBGPsec(keys, BGPsecRequest{Prefix: "2001:db8::/32", ASPath: path, Proof: proof, LocalASN: local}); got.Status != StatusValid {
		t.Fatalf("ECDSA chain failed: %+v", got.Reasons)
	}

	var _ ed25519.PublicKey
	var _ *ecdsa.PublicKey
}
