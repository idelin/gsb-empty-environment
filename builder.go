package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
)

// Recorder builds correctly authenticated negotiation transcripts for a
// single session. It is intended for protocol peers and for test fixtures;
// the Verifier is the authoritative checker.
type Recorder struct {
	sessionID []byte
	keyA      ed25519.PrivateKey
	keyB      ed25519.PrivateKey

	capsA []string
	capsB []string

	capsARaw []byte
	capsBRaw []byte
	selRaw   []byte

	records []Record
	built   bool
}

// NewRecorder constructs a Recorder bound to one session with both peer keys.
func NewRecorder(sessionID []byte, keyA, keyB ed25519.PrivateKey) (*Recorder, error) {
	if len(keyA) != ed25519.PrivateKeySize || len(keyB) != ed25519.PrivateKeySize {
		return nil, errors.New("netsec: recorder requires two Ed25519 private keys")
	}
	return &Recorder{
		sessionID: append([]byte(nil), sessionID...),
		keyA:      keyA,
		keyB:      keyB,
	}, nil
}

// OfferA records party A's capability list at slot 0.
func (r *Recorder) OfferA(algorithms []string) error {
	if r.capsARaw != nil {
		return errors.New("netsec: party A offer already recorded")
	}
	if duplicated(algorithms) {
		return errors.New("netsec: capability list contains duplicates")
	}
	r.capsA = append([]string(nil), algorithms...)
	r.capsARaw = EncodeCapabilities(TypeCapabilitiesA, algorithms)
	r.records = append(r.records, Record{
		Type: TypeCapabilitiesA,
		Raw:  r.capsARaw,
		Marks: []Mark{
			signRaw(r.keyA, r.sessionID, 0, r.capsARaw, TypeCapabilitiesA, PartyA),
		},
	})
	return nil
}

// OfferB records party B's capability list at slot 1.
func (r *Recorder) OfferB(algorithms []string) error {
	if r.capsBRaw != nil {
		return errors.New("netsec: party B offer already recorded")
	}
	if r.capsARaw == nil {
		return errors.New("netsec: party A offer must be recorded first")
	}
	if duplicated(algorithms) {
		return errors.New("netsec: capability list contains duplicates")
	}
	r.capsB = append([]string(nil), algorithms...)
	r.capsBRaw = EncodeCapabilities(TypeCapabilitiesB, algorithms)
	r.records = append(r.records, Record{
		Type: TypeCapabilitiesB,
		Raw:  r.capsBRaw,
		Marks: []Mark{
			signRaw(r.keyB, r.sessionID, 1, r.capsBRaw, TypeCapabilitiesB, PartyB),
		},
	})
	return nil
}

// Select records party B's choice at slot 2. The selection binds both
// observed capability byte strings.
func (r *Recorder) Select(chosen string) error {
	if r.selRaw != nil {
		return errors.New("netsec: selection already recorded")
	}
	if r.capsARaw == nil || r.capsBRaw == nil {
		return errors.New("netsec: both offers must be recorded before selection")
	}
	r.selRaw = EncodeSelection(chosen, r.capsARaw, r.capsBRaw)
	r.records = append(r.records, Record{
		Type: TypeSelection,
		Raw:  r.selRaw,
		Marks: []Mark{
			signRaw(r.keyB, r.sessionID, 2, r.selRaw, TypeSelection, PartyB),
		},
	})
	return nil
}

// Confirm records the mutually signed final confirmation at slot 3, binding
// the chain hash of the preceding three records.
func (r *Recorder) Confirm() error {
	if r.built {
		return errors.New("netsec: confirmation already recorded")
	}
	if r.selRaw == nil {
		return errors.New("netsec: selection must be recorded before confirmation")
	}
	chain := chainHashOf(r.capsARaw, r.capsBRaw, r.selRaw)
	confirmRaw := EncodeConfirmation(chain)
	r.records = append(r.records, Record{
		Type: TypeConfirmation,
		Raw:  confirmRaw,
		Marks: []Mark{
			signRaw(r.keyA, r.sessionID, 3, confirmRaw, TypeConfirmation, PartyA),
			signRaw(r.keyB, r.sessionID, 3, confirmRaw, TypeConfirmation, PartyB),
		},
	})
	r.built = true
	return nil
}

// Transcript returns a deep copy of the built records so callers cannot mutate
// the recorder's evidence through the returned slices.
func (r *Recorder) Transcript() []Record {
	out := make([]Record, len(r.records))
	for index, rec := range r.records {
		copied := Record{Type: rec.Type, Raw: append([]byte(nil), rec.Raw...)}
		copied.Marks = make([]Mark, len(rec.Marks))
		for markIndex, mark := range rec.Marks {
			copied.Marks[markIndex] = Mark{
				Signer:    mark.Signer,
				Signature: append([]byte(nil), mark.Signature...),
			}
		}
		out[index] = copied
	}
	return out
}

// ChainHash exposes the transcript hash embedded in the confirmation.
func (r *Recorder) ChainHash() ([sha256.Size]byte, error) {
	if r.selRaw == nil {
		return [sha256.Size]byte{}, errors.New("netsec: transcript not ready for chain hash")
	}
	return chainHashOf(r.capsARaw, r.capsBRaw, r.selRaw), nil
}
