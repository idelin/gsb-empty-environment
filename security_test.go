package netsec

import (
	"errors"
	"testing"
)

func TestRejectsUnverifiedSignatureChain(t *testing.T) {
	policy := testPolicy("alpha")

	resp := nsecNXResponse(t, "alpha", 3, 7)
	resp.Attestation.ChainVerified = false
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unverified chain: %v", err)
	}

	resp = nsecNXResponse(t, "alpha", 3, 7)
	resp.NSEC[0].RRSIG.Verified = false
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unverified RRset: %v", err)
	}

	resp = nsecNXResponse(t, "alpha", 3, 7)
	resp.Attestation.KeyGeneration = 2
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrKeyGeneration) {
		t.Fatalf("wrong key epoch: %v", err)
	}
}

func TestRejectsTamperedNSEC3ParametersAndClass(t *testing.T) {
	policy := testPolicy("alpha")
	params := ParamsNSEC3(2, []byte{0xaa, 0xbb})
	qname := MustParseName("ghost.example.")

	resp := nsec3NXResponse(t, "alpha", qname, params, 3, 7)
	resp.NSEC3[0].Salt[0] ^= 0xff
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrMalformed) {
		t.Fatalf("salt mismatch: %v", err)
	}

	resp = nsec3NXResponse(t, "alpha", qname, params, 3, 7)
	resp.NSEC3[0].Class = 3
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrMalformed) {
		t.Fatalf("class mismatch: %v", err)
	}

	resp = nsec3NXResponse(t, "alpha", qname, params, 3, 7)
	resp.NSEC3[1].Owner = MustParseName("not-a-hash.example.")
	resp.NSEC3[1].RRSIG.Signer = testApex(t)
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrMalformed) {
		t.Fatalf("bad base32hex owner: %v", err)
	}
}
