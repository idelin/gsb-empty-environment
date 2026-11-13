package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
)

// KeySchedule contains the directional traffic keys and the confirmation key.
// SendA encrypts/authenticates A-to-B traffic; SendB is B-to-A; Confirm is used
// only for the role-separated HMAC confirmations.
type KeySchedule struct {
	SendA   [32]byte
	SendB   [32]byte
	Confirm [32]byte
}

// Equal reports whether two schedules are identical.
func (k KeySchedule) Equal(other KeySchedule) bool {
	return hmac.Equal(k.SendA[:], other.SendA[:]) &&
		hmac.Equal(k.SendB[:], other.SendB[:]) &&
		hmac.Equal(k.Confirm[:], other.Confirm[:])
}

// HKDFSHA256 implements RFC 5869 extract-and-expand with SHA-256.
func HKDFSHA256(ikm, salt, info []byte, length int) ([]byte, error) {
	if length < 0 || length > 255*sha256.Size {
		return nil, fmt.Errorf("netsec: invalid HKDF length %d", length)
	}
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	extractor := hmac.New(sha256.New, salt)
	_, _ = extractor.Write(ikm)
	prk := extractor.Sum(nil)

	var output []byte
	var previous []byte
	for counter := byte(1); len(output) < length; counter++ {
		expander := hmac.New(sha256.New, prk)
		_, _ = expander.Write(previous)
		_, _ = expander.Write(info)
		_, _ = expander.Write([]byte{counter})
		previous = expander.Sum(nil)
		output = append(output, previous...)
	}
	return output[:length], nil
}

// DeriveKeySchedule derives send-A, send-B, and confirm keys from the canonical
// compressed shared point and the transcript digest.
func DeriveKeySchedule(sharedPointCompressed, transcriptDigest []byte) (KeySchedule, error) {
	if len(transcriptDigest) != sha256.Size {
		return KeySchedule{}, errors.New("netsec: transcript digest must be SHA-256")
	}
	_, _, canonical, err := ParseP256Point(sharedPointCompressed)
	if err != nil {
		return KeySchedule{}, fmt.Errorf("netsec: shared point: %w", err)
	}
	if !hmac.Equal(canonical, sharedPointCompressed) {
		return KeySchedule{}, errors.New("netsec: shared point is not canonical compressed encoding")
	}

	sendA, err := HKDFSHA256(canonical, transcriptDigest, []byte("send-A"), 32)
	if err != nil {
		return KeySchedule{}, err
	}
	sendB, err := HKDFSHA256(canonical, transcriptDigest, []byte("send-B"), 32)
	if err != nil {
		return KeySchedule{}, err
	}
	confirm, err := HKDFSHA256(canonical, transcriptDigest, []byte("confirm"), 32)
	if err != nil {
		return KeySchedule{}, err
	}
	var schedule KeySchedule
	copy(schedule.SendA[:], sendA)
	copy(schedule.SendB[:], sendB)
	copy(schedule.Confirm[:], confirm)
	return schedule, nil
}

const (
	confirmLabelA = "confirm/initiator-A"
	confirmLabelB = "confirm/responder-B"
)

func confirmationMAC(confirmKey [32]byte, role Role, transcriptDigest [32]byte) []byte {
	label := confirmLabelA
	if role == RoleB {
		label = confirmLabelB
	}
	input := appendLengthPrefixed(nil, []byte(label))
	input = appendLengthPrefixed(input, transcriptDigest[:])
	mac := hmac.New(sha256.New, confirmKey[:])
	_, _ = mac.Write(input)
	return mac.Sum(nil)
}
