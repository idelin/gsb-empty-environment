package treekem

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
)

// GroupContext is the epoch-binding context used by the key schedule.
type GroupContext struct {
	GroupID                 []byte
	Epoch                   uint64
	TreeHash                []byte
	ConfirmedTranscriptHash []byte
}

// Encode returns deterministic length-prefixed context bytes.
func (g GroupContext) Encode() []byte {
	hb := &hashBuilder{}
	hb.uint16(0x0001) // modeled protocol version
	hb.uint16(0x0001) // modeled SHA256/AES-GCM suite
	hb.bytes(g.GroupID)
	hb.uint64(g.Epoch)
	hb.bytes(g.TreeHash)
	hb.bytes(g.ConfirmedTranscriptHash)
	return append([]byte(nil), hb.buf...)
}

// EpochSecrets contains the RFC 9420 Section 8 derived secrets.
type EpochSecrets struct {
	EpochSecret          []byte
	SenderDataSecret     []byte
	EncryptionSecret     []byte
	ExporterSecret       []byte
	AuthenticationSecret []byte
	ExternalSecret       []byte
	ConfirmationKey      []byte
	MembershipKey        []byte
	ResumptionPSK        []byte
	InitSecret           []byte
}

// DeriveEpochSecrets advances joiner_secret -> epoch_secret and derives the
// standard named epoch secrets.
func DeriveEpochSecrets(joinerSecret []byte, context *GroupContext) (*EpochSecrets, error) {
	if len(joinerSecret) != hashSize {
		return nil, fmt.Errorf("treekem: joiner_secret must be %d bytes", hashSize)
	}
	if context == nil {
		return nil, errors.New("treekem: nil group context")
	}
	epochSecret, err := ExpandWithLabel(joinerSecret, "epoch", context.Encode(), hashSize)
	if err != nil {
		return nil, err
	}
	derive := func(label string) ([]byte, error) {
		return deriveSecret(epochSecret, label, nil)
	}

	secrets := &EpochSecrets{EpochSecret: epochSecret}
	if secrets.SenderDataSecret, err = derive("sender data"); err != nil {
		return nil, err
	}
	if secrets.EncryptionSecret, err = derive("encryption"); err != nil {
		return nil, err
	}
	if secrets.ExporterSecret, err = derive("exporter"); err != nil {
		return nil, err
	}
	if secrets.AuthenticationSecret, err = derive("authentication"); err != nil {
		return nil, err
	}
	if secrets.ExternalSecret, err = derive("external"); err != nil {
		return nil, err
	}
	if secrets.ConfirmationKey, err = derive("confirm"); err != nil {
		return nil, err
	}
	if secrets.MembershipKey, err = derive("membership"); err != nil {
		return nil, err
	}
	if secrets.ResumptionPSK, err = derive("resumption"); err != nil {
		return nil, err
	}
	if secrets.InitSecret, err = deriveSecret(epochSecret, "init", nil); err != nil {
		return nil, err
	}
	return secrets, nil
}

// JoinerSecret applies RFC's joiner_secret = Extract(init_secret, commit_secret).
func JoinerSecret(previousInitSecret, commitSecret []byte) []byte {
	return hkdfExtract(previousInitSecret, commitSecret)
}

// Transcript holds confirmed and interim transcript hashes.
type Transcript struct {
	Confirmed []byte
	Interim   []byte
}

// NewInitialTranscript returns the shared genesis transcript.
func NewInitialTranscript(groupID []byte) *Transcript {
	return &Transcript{
		Confirmed: hashBytes("treekem-initial-confirmed-v1", groupID),
		Interim:   hashBytes("treekem-initial-interim-v1", groupID),
	}
}

func (t *Transcript) advance(commitBytes, confirmation []byte) *Transcript {
	return &Transcript{
		Confirmed: hashBytes("treekem-confirmed-transcript-v1", t.Confirmed, commitBytes),
		Interim:   hashBytes("treekem-interim-transcript-v1", t.Interim, confirmation),
	}
}

func confirmationTag(confirmationKey, confirmedTranscriptHash []byte) []byte {
	mac := hmac.New(sha256.New, confirmationKey)
	_, _ = mac.Write(confirmedTranscriptHash)
	return mac.Sum(nil)
}

func verifyConfirmation(confirmationKey, confirmedTranscriptHash, tag []byte) bool {
	expected := confirmationTag(confirmationKey, confirmedTranscriptHash)
	return hmac.Equal(expected, tag)
}
