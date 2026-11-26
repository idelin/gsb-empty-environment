package pathsec

import "testing"

func TestForgedSignedPCountZeroStrongRejectsWeakAccepts(t *testing.T) {
	path := []uint32{65100, 65200}
	local := uint32(65000)
	pub1, priv1, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	pub2, priv2, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	keys := NewKeyStore().TrustEd25519(65100, pub1).TrustEd25519(65200, pub2)
	signers := map[uint32]*Signer{
		65100: NewEd25519Signer(65100, priv1),
		65200: NewEd25519Signer(65200, priv2),
	}
	proof, err := SignBGPsecChain("198.51.100.0/24", path, local, signers, []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	base := BGPsecRequest{
		Prefix:   "198.51.100.0/24",
		ASPath:   path,
		Proof:    proof,
		LocalASN: local,
	}
	if got := VerifyBGPsec(keys, base); got.Status != StatusInvalid {
		t.Fatalf("strong validator must reject signed pCount=0, got %s", got.Status)
	}
	weak := base
	weak.IgnorePCount = true
	if got := VerifyBGPsec(keys, weak); got.Status != StatusValid {
		t.Fatalf("weak pCount-ignoring validator should accept cryptographic chain: %+v", got.Reasons)
	}
}
