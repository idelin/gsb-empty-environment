package netsec

import (
	"crypto/ed25519"
	"errors"
)

// Well-known Stellar network passphrases.
const (
	PassphrasePublic  = "Public Global Stellar Network ; September 2015"
	PassphraseTestnet = "Test SDF Network ; September 2015"
)

var (
	// ErrUnknownSigner means no account record resolved for the layer source.
	ErrUnknownSigner = errors.New("netsec: unknown signer account")
	// ErrInvalidSignature means at least one signature failed ed25519
	// verification or used a public key that is not an authorized signer.
	ErrInvalidSignature = errors.New("netsec: invalid signature")
	// ErrThresholdNotMet means unique signer weight stayed below threshold.
	ErrThresholdNotMet = errors.New("netsec: signer threshold not met")
)

// Signer is one authorized signer key with its on-account weight.
type Signer struct {
	PublicKey []byte // 32-byte ed25519 key
	Weight    uint32
}

// Account holds the signers and threshold levels of a Stellar account.
type Account struct {
	Signers []Signer
	Low     uint32
	Medium  uint32
	High    uint32
}

// AccountLookup resolves the 32-byte source account id of a layer to its
// current account record. Returning (nil, false) fails verification closed.
type AccountLookup func(accountID []byte) (*Account, bool)

// SignatureOutcome reports verification detail for one envelope layer.
type SignatureOutcome struct {
	EnvelopeType        uint32
	SourceAccount       []byte
	TotalSignatures     int
	UniqueSigners       [][]byte
	DuplicateSignatures int
	RejectedSignatures  int
	Weight              uint32
	Threshold           uint32
	Passed              bool
}

// VerifyResult covers both layers of a fee bump envelope. For ordinary
// transactions only Outer is populated.
type VerifyResult struct {
	Outer  SignatureOutcome
	Inner  *SignatureOutcome
	Passed bool
}

// Verifier binds a network passphrase, signer directory, and threshold policy.
type Verifier struct {
	NetworkPassphrase string
	Accounts          AccountLookup
	// Threshold selects the required level. Defaults to low when zero.
	Threshold func(account *Account) uint32
}

// VerifyEnvelope validates every supplied signature cryptographically, counts
// each public key at most once, and requires the caller-selected threshold.
// Fee bump envelopes must pass at both the outer and inner layers.
func (v *Verifier) VerifyEnvelope(env *Envelope) (*VerifyResult, error) {
	if v == nil || v.Accounts == nil || v.NetworkPassphrase == "" {
		return nil, errors.New("netsec: verifier not configured")
	}
	result := &VerifyResult{}
	outer, err := v.verifyLayer(env)
	if err != nil {
		return nil, err
	}
	result.Outer = outer
	if !outer.Passed {
		return result, ErrThresholdNotMet
	}

	if env.Inner != nil {
		inner, err := v.verifyLayer(env.Inner)
		if err != nil {
			return nil, err
		}
		result.Inner = &inner
		if !inner.Passed {
			return result, ErrThresholdNotMet
		}
	}

	result.Passed = true
	return result, nil
}

func (v *Verifier) verifyLayer(env *Envelope) (SignatureOutcome, error) {
	out := SignatureOutcome{
		EnvelopeType:  env.Type,
		SourceAccount: append([]byte(nil), env.SourceAccount...),
	}

	account, ok := v.Accounts(env.SourceAccount)
	if !ok || account == nil {
		return out, ErrUnknownSigner
	}
	out.Threshold = v.requiredThreshold(account)

	// Index signers by hint for preselection; every surviving signature still
	// undergoes full ed25519 verification against the digest.
	byHint := make(map[uint32][]*Signer)
	for i := range account.Signers {
		s := &account.Signers[i]
		if len(s.PublicKey) != ed25519PublicKeySize {
			continue
		}
		hint := hintFromKey(s.PublicKey)
		byHint[hint] = append(byHint[hint], s)
	}

	digest := PayloadDigest(v.NetworkPassphrase, env.Type, env.TransactionXDR)
	out.TotalSignatures = len(env.Signatures)

	seen := make(map[string]struct{})
	for _, ds := range env.Signatures {
		hint := hintFromBytes(ds.Hint[:])
		candidates := byHint[hint]

		var matched *Signer
		for _, candidate := range candidates {
			if ed25519.Verify(ed25519.PublicKey(candidate.PublicKey), digest[:], ds.Signature) {
				matched = candidate
				break
			}
		}

		if matched == nil {
			// Hint collision or forged hint: no authorized key verifies, fail closed.
			out.RejectedSignatures++
			return out, ErrInvalidSignature
		}

		key := string(matched.PublicKey)
		if _, duplicate := seen[key]; duplicate {
			// Repeating the same public key never adds weight.
			out.DuplicateSignatures++
			continue
		}
		seen[key] = struct{}{}
		out.UniqueSigners = append(out.UniqueSigners, append([]byte(nil), matched.PublicKey...))
		out.Weight += matched.Weight
	}

	out.Passed = out.Weight >= out.Threshold
	return out, nil
}

func (v *Verifier) requiredThreshold(account *Account) uint32 {
	if v.Threshold != nil {
		return v.Threshold(account)
	}
	return account.Low
}

// hintFromKey returns the signature hint: the trailing four bytes of a key.
func hintFromKey(publicKey []byte) uint32 {
	return hintFromBytes(publicKey[len(publicKey)-4:])
}

func hintFromBytes(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// SelectByHint returns the signer candidates whose trailing four key bytes
// match the hint. It is exported as a preselection helper for callers that
// want to shortlist keys before invoking VerifyEnvelope; it never verifies.
func SelectByHint(account *Account, hint [4]byte) []Signer {
	var out []Signer
	want := hintFromBytes(hint[:])
	for _, s := range account.Signers {
		if len(s.PublicKey) == ed25519PublicKeySize && hintFromKey(s.PublicKey) == want {
			out = append(out, s)
		}
	}
	return out
}
