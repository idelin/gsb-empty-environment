package migration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// SignatureVerifier independently authenticates canonical evidence payloads.
type SignatureVerifier interface {
	Verify(keyID string, payload, signature []byte) bool
}

// SegmentPayload returns the deterministic bytes signed by an old device.
func SegmentPayload(segment Segment) []byte {
	parts := []string{
		"migration-segment:v1",
		segment.ID,
		segment.OldDeviceID,
		segment.NewDeviceID,
		strconv.FormatUint(uint64(segment.Old.Start), 10),
		strconv.FormatUint(uint64(segment.Old.End), 10),
		strconv.FormatUint(uint64(segment.New.Start), 10),
		strconv.FormatUint(uint64(segment.New.End), 10),
		string(segment.Orientation),
	}
	return []byte(strings.Join(parts, "\n"))
}

// ReceiptPayload returns the deterministic bytes signed by the emitting device.
func ReceiptPayload(receipt Receipt) []byte {
	parts := []string{
		"migration-receipt:v1",
		receipt.RequestID,
		string(receipt.Side),
		receipt.DeviceID,
		strconv.FormatUint(uint64(receipt.Steps.Start), 10),
		strconv.FormatUint(uint64(receipt.Steps.End), 10),
	}
	return []byte(strings.Join(parts, "\n"))
}

// MemorySigner is an ed25519 signer intended for hosts and tests.
type MemorySigner struct {
	privateKeys map[string]ed25519.PrivateKey
	publicKeys  map[string]ed25519.PublicKey
}

// NewMemorySigner creates an empty in-memory signer.
func NewMemorySigner() *MemorySigner {
	return &MemorySigner{
		privateKeys: make(map[string]ed25519.PrivateKey),
		publicKeys:  make(map[string]ed25519.PublicKey),
	}
}

// GenerateKey creates an ed25519 key for keyID.
func (s *MemorySigner) GenerateKey(keyID string) error {
	if keyID == "" {
		return fmt.Errorf("key id must not be empty")
	}
	if _, exists := s.publicKeys[keyID]; exists {
		return fmt.Errorf("key %q already exists", keyID)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	s.privateKeys[keyID] = private
	s.publicKeys[keyID] = public
	return nil
}

// Sign signs payload with an existing private key.
func (s *MemorySigner) Sign(keyID string, payload []byte) (Signature, error) {
	private, ok := s.privateKeys[keyID]
	if !ok {
		return Signature{}, fmt.Errorf("private key %q is unavailable", keyID)
	}
	value := ed25519.Sign(private, payload)
	return Signature{KeyID: keyID, Value: value}, nil
}

// SignSegment signs a segment payload.
func (s *MemorySigner) SignSegment(segment Segment) (Segment, error) {
	signature, err := s.Sign(segment.Signature.KeyID, SegmentPayload(segment))
	if err != nil {
		return Segment{}, err
	}
	segment.Signature = signature
	return segment, nil
}

// SignReceipt signs a receipt payload.
func (s *MemorySigner) SignReceipt(receipt Receipt) (Receipt, error) {
	signature, err := s.Sign(receipt.Signature.KeyID, ReceiptPayload(receipt))
	if err != nil {
		return Receipt{}, err
	}
	receipt.Signature = signature
	return receipt, nil
}

// Verify checks a signature with an existing public key.
func (s *MemorySigner) Verify(keyID string, payload, signature []byte) bool {
	public, ok := s.publicKeys[keyID]
	if !ok {
		return false
	}
	return ed25519.Verify(public, payload, signature)
}

// EncodeSignature returns a portable base64 signature representation.
func EncodeSignature(signature Signature) string {
	return signature.KeyID + ":" + base64.StdEncoding.EncodeToString(signature.Value)
}
