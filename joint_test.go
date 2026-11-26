package pathsec

import "testing"

func TestJointVerifyAllValid(t *testing.T) {
	path := []uint32{65100}
	local := uint32(65000)
	pub, priv, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	roas := NewROATable().MustAdd(ROA{Prefix: "203.0.113.0/24", MaxLength: 24, OriginASN: 65100})
	aspa := NewASPATable()
	keys := NewKeyStore().TrustEd25519(65100, pub)
	proof, err := SignBGPsecChain("203.0.113.0/24", path, local,
		map[uint32]*Signer{65100: NewEd25519Signer(65100, priv)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	validator := NewJointValidator(roas, aspa, keys)
	validator.Config = StrictConfig()
	result := validator.Verify(VerifyRequest{
		Prefix:   "203.0.113.0/24",
		ASPath:   path,
		Role:     RoleProvider,
		LocalASN: local,
		BGPsec:   proof,
	})
	if result.Status != StatusValid {
		t.Fatalf("expected joint Valid, got %s: roa=%s aspa=%s bgpsec=%s reasons=%+v",
			result.Status, result.ROA.Status, result.ASPA.Status, result.BGPsec.Status, result.Reasons)
	}
	if !result.Valid || result.Overall != StatusValid {
		t.Fatalf("boolean/overall mismatch: %+v", result)
	}
}

func TestBGPsecPrependPCountMultiplicity(t *testing.T) {
	rawPath := []uint32{65100, 65100, 65200}
	normalized := NormalizeASNs(rawPath)
	if len(normalized.ASNs) != 2 || normalized.Runs[0] != 2 {
		t.Fatalf("bad normalization: %+v", normalized)
	}
	local := uint32(65000)
	pub1, priv1, _ := GenerateEd25519Key()
	pub2, priv2, _ := GenerateEd25519Key()
	keys := NewKeyStore().TrustEd25519(65100, pub1).TrustEd25519(65200, pub2)
	signers := map[uint32]*Signer{
		65100: NewEd25519Signer(65100, priv1),
		65200: NewEd25519Signer(65200, priv2),
	}
	proof, err := SignBGPsecChain("192.0.2.0/24", rawPath, local, signers, []int{2, 1})
	if err != nil {
		t.Fatal(err)
	}
	result := VerifyBGPsec(keys, BGPsecRequest{
		Prefix:           "192.0.2.0/24",
		ASPath:           normalized.ASNs,
		Normalized:       normalized,
		Proof:            proof,
		LocalASN:         local,
		EnforceRunCounts: true,
	})
	if result.Status != StatusValid {
		t.Fatalf("expected pCount multiplicity valid: %+v", result.Reasons)
	}
}
