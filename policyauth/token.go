package policyauth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Token is a signed policy authorization presented before unsealing.
type Token struct {
	// ApprovedPolicy is the PCR policy digest (TPM approvedPolicy) that
	// this token authorizes.
	ApprovedPolicy Digest
	// PolicyRef binds the authorization to a device and a sealed object.
	PolicyRef PolicyRef
	// Generation is the monotonic authorization algebra. A token whose
	// generation is below the persisted NV counter is a replayed generation
	// and must be rejected.
	Generation uint64
	// Signature is the ASN.1-encoded ECDSA P-256 signature over the
	// canonical signed message.
	Signature []byte
}

// signedMessage builds the authenticated message for a token:
//
//	approvedPolicy || policyRef || generation
//
// approvedPolicy||policyRef is the coverage the authorization scheme
// requires. The generation is included in the same signed message so an
// attacker cannot relabel an old, otherwise-valid signature with a newer
// generation to bypass the monotonic-counter check.
func signedMessage(approved Digest, ref PolicyRef, generation uint64) ([]byte, error) {
	refBytes, err := ref.MarshalBinary()
	if err != nil {
		return nil, err
	}
	var generationBytes [8]byte
	binary.BigEndian.PutUint64(generationBytes[:], generation)

	var buf bytes.Buffer
	buf.Write(approved[:])
	buf.Write(refBytes)
	buf.Write(generationBytes[:])
	return buf.Bytes(), nil
}

// SignToken produces an authorization token signed by the authority key.
func SignToken(priv *ecdsa.PrivateKey, approved Digest, ref PolicyRef, generation uint64) (*Token, error) {
	if priv == nil {
		return nil, errors.New("policyauth: nil signing key")
	}
	message, err := signedMessage(approved, ref, generation)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(message)
	signature, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		return nil, fmt.Errorf("policyauth: sign token: %w", err)
	}
	return &Token{
		ApprovedPolicy: approved,
		PolicyRef:      ref,
		Generation:     generation,
		Signature:      signature,
	}, nil
}

// verifySignature checks the token signature against the given public key.
func verifySignature(token *Token, pub *ecdsa.PublicKey) error {
	message, err := signedMessage(token.ApprovedPolicy, token.PolicyRef, token.Generation)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(message)
	if !ecdsa.VerifyASN1(pub, digest[:], token.Signature) {
		return ErrBadSignature
	}
	return nil
}
