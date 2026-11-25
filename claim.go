package handoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"sort"
)

// Digest is a SHA-256 content digest.
type Digest [sha256.Size]byte

// ParseDigest parses a hexadecimal SHA-256 digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	b, err := hex.DecodeString(s)
	if err != nil {
		return d, err
	}
	if len(b) != sha256.Size {
		return d, ErrDigestLength
	}
	copy(d[:], b)
	return d, nil
}

// String returns lowercase hex.
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// MarshalText encodes the digest as lowercase hex.
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText parses a hexadecimal digest.
func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// IsZero reports whether d is the all-zero digest.
func (d Digest) IsZero() bool { return d == Digest{} }

// Claim is one exact prefix origin declaration. Exclusions are holes removed
// from Prefix before longest-prefix matching.
type Claim struct {
	Prefix     netip.Prefix   `json:"prefix"`
	Tenant     TenantID       `json:"tenant"`
	Exclusions []netip.Prefix `json:"exclusions,omitempty"`
}

// Canonical returns a masked claim with sorted, masked exclusions.
func (c Claim) Canonical() Claim {
	out := Claim{Prefix: c.Prefix.Masked(), Tenant: c.Tenant}
	for _, e := range c.Exclusions {
		out.Exclusions = append(out.Exclusions, e.Masked())
	}
	sort.Slice(out.Exclusions, func(i, j int) bool {
		return out.Exclusions[i].Bits() < out.Exclusions[j].Bits() ||
			(out.Exclusions[i].Bits() == out.Exclusions[j].Bits() &&
				out.Exclusions[i].Addr().Less(out.Exclusions[j].Addr()))
	})
	return out
}

type claimDTO struct {
	Prefix     string   `json:"prefix"`
	Tenant     string   `json:"tenant"`
	Exclusions []string `json:"exclusions,omitempty"`
}

func (c Claim) dto() claimDTO {
	c = c.Canonical()
	d := claimDTO{Prefix: c.Prefix.String(), Tenant: string(c.Tenant)}
	for _, e := range c.Exclusions {
		d.Exclusions = append(d.Exclusions, e.String())
	}
	return d
}

// Digest returns the exact claim digest. Two semantically equal claims have
// the same digest after canonicalization.
func (c Claim) Digest() Digest {
	b, _ := json.Marshal(c.dto())
	return sha256.Sum256(append([]byte("handoff-claim-v1\n"), b...))
}

// Covers reports whether addr belongs to the prefix and not an exclusion hole.
func (c Claim) Covers(addr netip.Addr) bool {
	c = c.Canonical()
	if !c.Prefix.Contains(addr) {
		return false
	}
	for _, hole := range c.Exclusions {
		if hole.Contains(addr) {
			return false
		}
	}
	return true
}

// Statement is an exact atomic update. One receipt for Statement.Digest proves
// that the issuing egress observed this particular merged batch.
type Statement struct {
	PlanID     string   `json:"plan_id"`
	ID         string   `json:"id"`
	Generation uint64   `json:"generation"`
	Announces  []Claim  `json:"announces,omitempty"`
	Withdraws  []Digest `json:"withdraws,omitempty"`
	Nonce      uint64   `json:"nonce,omitempty"`
}

// Canonical returns a deterministic copy.
func (s Statement) Canonical() Statement {
	out := Statement{
		PlanID:     s.PlanID,
		ID:         s.ID,
		Generation: s.Generation,
		Nonce:      s.Nonce,
	}
	for _, c := range s.Announces {
		out.Announces = append(out.Announces, c.Canonical())
	}
	sort.Slice(out.Announces, func(i, j int) bool {
		di, dj := out.Announces[i].Digest(), out.Announces[j].Digest()
		return hex.EncodeToString(di[:]) < hex.EncodeToString(dj[:])
	})
	out.Withdraws = append([]Digest(nil), s.Withdraws...)
	sort.Slice(out.Withdraws, func(i, j int) bool {
		return out.Withdraws[i].String() < out.Withdraws[j].String()
	})
	return out
}

type statementDTO struct {
	Schema     string     `json:"schema"`
	PlanID     string     `json:"plan_id"`
	ID         string     `json:"id"`
	Generation uint64     `json:"generation"`
	Announces  []claimDTO `json:"announces,omitempty"`
	Withdraws  []string   `json:"withdraws,omitempty"`
	Nonce      uint64     `json:"nonce,omitempty"`
}

// Digest returns the exact merged-statement digest.
func (s Statement) Digest() Digest {
	s = s.Canonical()
	d := statementDTO{
		Schema:     "handoff-statement-v1",
		PlanID:     s.PlanID,
		ID:         s.ID,
		Generation: s.Generation,
		Nonce:      s.Nonce,
	}
	for _, c := range s.Announces {
		d.Announces = append(d.Announces, c.dto())
	}
	for _, w := range s.Withdraws {
		d.Withdraws = append(d.Withdraws, w.String())
	}
	b, _ := json.Marshal(d)
	return sha256.Sum256(b)
}

// ClaimDigests returns digests of all announced claims.
func (s Statement) ClaimDigests() []Digest {
	out := make([]Digest, 0, len(s.Announces))
	for _, c := range s.Announces {
		out = append(out, c.Digest())
	}
	return out
}
