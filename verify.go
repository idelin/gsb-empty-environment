package netsec

import (
	"bytes"
	"fmt"
	"time"
)

// VerifyConfig pins the context in which a chain must be valid.
type VerifyConfig struct {
	// KeyRing holds the trusted hop public keys. Required.
	KeyRing *KeyRing
	// ExpectedChallenge is the connection nonce issued for this connection.
	ExpectedChallenge string
	// ExpectedAudience is the final target service identity.
	ExpectedAudience string
	// ExpectedClient is the client identity the root hop must have verified.
	ExpectedClient string
	// Now is the evaluation time. Zero means time.Now.
	Now time.Time
	// ExpectedHops optionally pins the exact hop sequence by HopID.
	ExpectedHops []string
	// MinAssurance optionally requires the chain's effective assurance to be
	// at least this level.
	MinAssurance Assurance
}

// HopInfo is one verifier-accepted attestation.
type HopInfo struct {
	Index           int
	HopID           string
	KeyID           string
	VerifiedSubject string
	Assurance       Assurance
	UpstreamMin     Assurance
	NotBefore       time.Time
	NotAfter        time.Time
}

// VerifiedChain is the successful verification result.
type VerifiedChain struct {
	// ClientID is the identity verified by the root TLS termination hop.
	ClientID string
	// EffectiveAssurance is the minimum assurance over every hop, which is
	// the strongest guarantee the whole chain can support.
	EffectiveAssurance Assurance
	// FirstVerifier is the root hop that actually verified the client.
	FirstVerifier string
	Hops          []HopInfo
}

// Verify validates every signature and binding rule in the chain and returns
// the accepted facts. It rejects broken links, cross-connection splicing,
// audience or identity mixing, expired attestations and any attempt to raise
// the upstream assurance level.
func Verify(c *Chain, cfg VerifyConfig) (*VerifiedChain, error) {
	if cfg.KeyRing == nil {
		return nil, fmt.Errorf("%w: key ring is required", ErrUnknownKey)
	}
	if c == nil || len(c.attestations) == 0 {
		return nil, ErrEmptyChain
	}
	now := cfg.Now
	if now.IsZero() {
		now = time.Now()
	}

	result := &VerifiedChain{Hops: make([]HopInfo, 0, len(c.attestations))}
	var runningMin Assurance = AssuranceValidChainAndIdentity
	var clientID string
	seenHops := make(map[string]bool, len(c.attestations))

	for i, a := range c.attestations {
		if err := validateShape(a, i); err != nil {
			return nil, err
		}

		entry, err := cfg.KeyRing.lookup(a.KeyID)
		if err != nil {
			return nil, err
		}
		if entry.hopID != a.HopID {
			return nil, fmt.Errorf("%w: key %q belongs to %q, not %q",
				ErrHopIdentityMismatch, a.KeyID, entry.hopID, a.HopID)
		}
		if seenHops[a.HopID] {
			return nil, fmt.Errorf("hop %d (%s): %w: hop appears more than once",
				i, a.HopID, ErrHopIdentityMismatch)
		}
		if err := verifySignature(a, entry); err != nil {
			return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, err)
		}

		var expectedPrev []byte
		if i == 0 {
			genesis := GenesisDigest(a.Audience, a.Challenge)
			expectedPrev = genesis[:]
		} else {
			tailDigest, err := AttestationDigest(c.attestations[i-1])
			if err != nil {
				return nil, err
			}
			expectedPrev = tailDigest[:]
		}
		if !bytes.Equal(a.PrevDigest, expectedPrev) {
			return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, ErrBrokenChain)
		}

		if cfg.ExpectedChallenge != "" && a.Challenge != cfg.ExpectedChallenge {
			return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, ErrChallengeMismatch)
		}
		if cfg.ExpectedAudience != "" && a.Audience != cfg.ExpectedAudience {
			return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, ErrAudienceMismatch)
		}

		if i == 0 {
			if a.UpstreamMin != AssuranceNone {
				return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, ErrAssuranceElevation)
			}
			clientID = a.VerifiedSubject
			if cfg.ExpectedClient != "" && clientID != cfg.ExpectedClient {
				return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, ErrIdentityMismatch)
			}
			result.FirstVerifier = a.HopID
		} else {
			prev := c.attestations[i-1]
			if a.VerifiedSubject != prev.HopID {
				return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, ErrIdentityMismatch)
			}
			// Defense in depth against segment grafting between two chains
			// that reuse hop IDs: the verified upstream identity must be a
			// hop that actually appeared earlier in THIS chain, not merely
			// an equal-looking ID minted inside another connection.
			if !seenHops[a.VerifiedSubject] {
				return nil, fmt.Errorf("hop %d (%s): %w: subject %q is not in this chain",
					i, a.HopID, ErrIdentityMismatch, a.VerifiedSubject)
			}
			// The hop must honestly echo the running minimum; it can neither
			// inflate nor deflate the upstream guarantee.
			if a.UpstreamMin != runningMin {
				return nil, fmt.Errorf("hop %d (%s): %w: upstream min %q, recorded %q",
					i, a.HopID, ErrAssuranceElevation, runningMin, a.UpstreamMin)
			}
		}

		if now.Before(a.NotBefore) {
			return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, ErrNotYetValid)
		}
		if now.After(a.NotAfter) {
			return nil, fmt.Errorf("hop %d (%s): %w", i, a.HopID, ErrExpired)
		}

		if i == 0 || a.Assurance < runningMin {
			runningMin = a.Assurance
		}
		seenHops[a.HopID] = true

		if len(cfg.ExpectedHops) > 0 {
			if i >= len(cfg.ExpectedHops) || a.HopID != cfg.ExpectedHops[i] {
				return nil, fmt.Errorf("hop %d: %w", i, ErrHopIdentityMismatch)
			}
		}

		result.Hops = append(result.Hops, HopInfo{
			Index:           a.Index,
			HopID:           a.HopID,
			KeyID:           a.KeyID,
			VerifiedSubject: a.VerifiedSubject,
			Assurance:       a.Assurance,
			UpstreamMin:     a.UpstreamMin,
			NotBefore:       a.NotBefore,
			NotAfter:        a.NotAfter,
		})
	}

	if len(cfg.ExpectedHops) != 0 && len(cfg.ExpectedHops) != len(c.attestations) {
		return nil, fmt.Errorf("%w: expected %d hops, got %d",
			ErrHopIdentityMismatch, len(cfg.ExpectedHops), len(c.attestations))
	}

	result.ClientID = clientID
	result.EffectiveAssurance = runningMin
	if cfg.MinAssurance != AssuranceNone && result.EffectiveAssurance < cfg.MinAssurance {
		return nil, fmt.Errorf("%w: effective %q below required %q",
			ErrAssuranceElevation, result.EffectiveAssurance, cfg.MinAssurance)
	}
	return result, nil
}
