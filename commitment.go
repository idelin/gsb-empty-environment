package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

const (
	// MaxParticipants is the largest participant set accepted by a round.
	MaxParticipants = 32
	// NonceSize is the exact size of a participant nonce.
	NonceSize = 32
	// CommitmentSize is the size of a SHA-256 commitment.
	CommitmentSize = sha256.Size
)

// NewNonce returns a cryptographically random NonceSize-byte nonce.
func NewNonce() ([NonceSize]byte, error) {
	var nonce [NonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nonce, fmt.Errorf("netsec: generate nonce: %w", err)
	}
	return nonce, nil
}

// Commitment computes the commitment for a round, participant, and nonce.
//
// The encoding is
//
//	SHA256(uint32be(len(round)) || round ||
//	       uint32be(len(participant)) || participant ||
//	       nonce)
//
// Lengths are byte lengths encoded in big-endian order. The nonce must be
// exactly NonceSize bytes.
func Commitment(roundID, participant string, nonce []byte) ([CommitmentSize]byte, error) {
	var out [CommitmentSize]byte
	if roundID == "" {
		return out, fmt.Errorf("%w: empty round ID", ErrInvalidConfig)
	}
	if participant == "" {
		return out, fmt.Errorf("%w: empty participant ID", ErrInvalidParticipant)
	}
	if uint64(len(roundID)) > math.MaxUint32 {
		return out, fmt.Errorf("%w: round ID is too long", ErrInvalidConfig)
	}
	if uint64(len(participant)) > math.MaxUint32 {
		return out, fmt.Errorf("%w: participant ID is too long", ErrInvalidParticipant)
	}
	if len(nonce) != NonceSize {
		return out, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidNonce, len(nonce), NonceSize)
	}

	h := sha256.New()
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(roundID)))
	_, _ = h.Write(prefix[:])
	_, _ = h.Write([]byte(roundID))
	binary.BigEndian.PutUint32(prefix[:], uint32(len(participant)))
	_, _ = h.Write(prefix[:])
	_, _ = h.Write([]byte(participant))
	_, _ = h.Write(nonce)
	copy(out[:], h.Sum(nil))
	return out, nil
}

func resultDigest(participants []string, reveals map[string][NonceSize]byte) [sha256.Size]byte {
	h := sha256.New()
	for _, participant := range participants {
		nonce := reveals[participant]
		_, _ = h.Write(nonce[:])
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}
