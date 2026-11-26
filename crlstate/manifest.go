package crlstate

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrInvalidManifest   = errors.New("crlstate: invalid manifest")
	ErrInvalidMigration  = errors.New("crlstate: invalid migration")
	ErrBadSignature      = errors.New("crlstate: invalid signature")
	ErrEpochRegression   = errors.New("crlstate: revocation epoch regression")
	ErrIssuerLocked      = errors.New("crlstate: issuer locked by fork")
	ErrDuplicateEntry    = errors.New("crlstate: duplicate serial entry in manifest")
	ErrDuplicateManifest = errors.New("crlstate: duplicate manifest")
)

func validReasonCode(c ReasonCode) bool {
	switch c {
	case ReasonUnspecified,
		ReasonKeyCompromise,
		ReasonCACompromise,
		ReasonAffiliationChanged,
		ReasonSuperseded,
		ReasonCessationOfOperation,
		ReasonCertificateHold,
		ReasonRemoveFromCRL,
		ReasonPrivilegeWithdrawn,
		ReasonAACompromise:
		return true
	}
	return false
}

func normalizeManifest(m *Manifest) {
	if m.SignerID == "" {
		m.SignerID = m.IssuerID
	}
	if m.PartitionID == "" {
		m.PartitionID = fmt.Sprintf("%s|%d|%s|%s|%#x", m.IssuerID, m.Epoch, m.SerialLo, m.SerialHi, uint64(m.Reasons))
	}
	sort.Slice(m.Entries, func(i, j int) bool {
		if c := m.Entries[i].Serial.Cmp(m.Entries[j].Serial); c != 0 {
			return c < 0
		}
		return m.Entries[i].Reason < m.Entries[j].Reason
	})
}

// ValidateManifest checks structural invariants. Signature authenticity is
// checked separately by Aggregator with its Verifier.
func ValidateManifest(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil", ErrInvalidManifest)
	}
	normalizeManifest(m)
	if m.IssuerID == "" {
		return fmt.Errorf("%w: empty issuer", ErrInvalidManifest)
	}
	if m.RootID == "" {
		return fmt.Errorf("%w: empty root", ErrInvalidManifest)
	}
	if m.PartitionID == "" {
		return fmt.Errorf("%w: empty partition id", ErrInvalidManifest)
	}
	if m.SerialLo.Cmp(m.SerialHi) >= 0 {
		return fmt.Errorf("%w: serial range [%s,%s) is empty or inverted", ErrInvalidManifest, m.SerialLo, m.SerialHi)
	}
	if m.Reasons == 0 {
		return fmt.Errorf("%w: empty reason mask", ErrInvalidManifest)
	}
	if m.Reasons&^AllStatusReasons != 0 {
		return fmt.Errorf("%w: reason mask %s contains a non-status bit", ErrInvalidManifest, m.Reasons)
	}
	if m.ValidFrom.IsZero() || m.ValidUntil.IsZero() || !m.ValidFrom.Before(m.ValidUntil) {
		return fmt.Errorf("%w: invalid validity window [%s,%s)", ErrInvalidManifest, m.ValidFrom, m.ValidUntil)
	}
	if m.IsDelta {
		if m.BaseDelta == 0 {
			return fmt.Errorf("%w: delta has no base delta dependency", ErrInvalidManifest)
		}
		if m.CRLNumber <= m.BaseDelta {
			return fmt.Errorf("%w: delta CRL number %d is not greater than base %d", ErrInvalidManifest, m.CRLNumber, m.BaseDelta)
		}
	} else if m.BaseDelta != 0 {
		return fmt.Errorf("%w: full manifest must not declare a base delta", ErrInvalidManifest)
	}
	if err := validateDelegationChain(m); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(m.Entries))
	for i, e := range m.Entries {
		if !validReasonCode(e.Reason) {
			return fmt.Errorf("%w: entry %d has unknown reason code %d", ErrInvalidManifest, i, e.Reason)
		}
		if e.Serial.Cmp(m.SerialLo) < 0 || e.Serial.Cmp(m.SerialHi) >= 0 {
			return fmt.Errorf("%w: entry serial %s outside partition [%s,%s)", ErrInvalidManifest, e.Serial, m.SerialLo, m.SerialHi)
		}
		if e.RevokedAt.IsZero() {
			return fmt.Errorf("%w: entry %s has zero revocation time", ErrInvalidManifest, e.Serial)
		}
		switch e.Reason {
		case ReasonRemoveFromCRL:
			if !m.IsDelta {
				return fmt.Errorf("%w: removeFromCRL entry is only valid in a delta", ErrInvalidManifest)
			}
			if !m.Reasons.Has(ReasonCertificateHold) {
				return fmt.Errorf("%w: removeFromCRL requires certificateHold in partition reasons", ErrInvalidManifest)
			}
		default:
			if !m.Reasons.Has(e.Reason) {
				return fmt.Errorf("%w: entry reason %d not covered by partition mask %s", ErrInvalidManifest, e.Reason, m.Reasons)
			}
		}
		key := e.Serial.String()
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: %s", ErrDuplicateEntry, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateDelegationChain(m *Manifest) error {
	if len(m.Indirect) == 0 {
		if m.SignerID != m.IssuerID {
			return fmt.Errorf("%w: indirect signer %q has no delegation chain", ErrInvalidManifest, m.SignerID)
		}
		return nil
	}
	cur := m.IssuerID
	for i, d := range m.Indirect {
		if d.Parent == "" || d.Child == "" {
			return fmt.Errorf("%w: delegation %d has empty parent or child", ErrInvalidManifest, i)
		}
		if d.Parent != cur {
			return fmt.Errorf("%w: delegation %d starts at %q, expected %q", ErrInvalidManifest, i, d.Parent, cur)
		}
		if d.Root != "" && d.Root != m.RootID {
			return fmt.Errorf("%w: delegation %d root %q does not match manifest root %q", ErrInvalidManifest, i, d.Root, m.RootID)
		}
		if d.SerialLo.Cmp(d.SerialHi) >= 0 {
			return fmt.Errorf("%w: delegation %d has invalid serial scope", ErrInvalidManifest, i)
		}
		if d.Reasons == 0 || d.Reasons&^AllStatusReasons != 0 {
			return fmt.Errorf("%w: delegation %d has invalid reason scope", ErrInvalidManifest, i)
		}
		if d.SerialLo.Cmp(m.SerialLo) > 0 || d.SerialHi.Cmp(m.SerialHi) < 0 {
			return fmt.Errorf("%w: delegation %d serial scope does not contain manifest", ErrInvalidManifest, i)
		}
		if !d.Reasons.HasAll(m.Reasons) {
			return fmt.Errorf("%w: delegation %d reason scope does not contain manifest", ErrInvalidManifest, i)
		}
		if d.ValidFrom.IsZero() || d.ValidUntil.IsZero() ||
			d.ValidFrom.After(m.ValidFrom) || d.ValidUntil.Before(m.ValidUntil) {
			return fmt.Errorf("%w: delegation %d validity does not contain manifest", ErrInvalidManifest, i)
		}
		cur = d.Child
	}
	if cur != m.SignerID {
		return fmt.Errorf("%w: delegation chain ends at %q, signer is %q", ErrInvalidManifest, cur, m.SignerID)
	}
	return nil
}

// ValidateMigration checks structural invariants, including exact serial×
// reason boundary equality between the old and new partition sets.
func ValidateMigration(m *Migration) error {
	if m == nil {
		return fmt.Errorf("%w: nil", ErrInvalidMigration)
	}
	if m.IssuerID == "" {
		return fmt.Errorf("%w: empty issuer", ErrInvalidMigration)
	}
	if m.OldRoot == "" || m.NewRoot == "" {
		return fmt.Errorf("%w: old and new roots are required", ErrInvalidMigration)
	}
	if m.Kind != MigrationSplit && m.Kind != MigrationMerge {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidMigration, m.Kind)
	}
	if m.ToEpoch < m.FromEpoch {
		return fmt.Errorf("%w: epoch regression from %d to %d", ErrInvalidMigration, m.FromEpoch, m.ToEpoch)
	}
	if m.EffectiveAt.IsZero() {
		return fmt.Errorf("%w: zero effective time", ErrInvalidMigration)
	}
	if len(m.Old) == 0 || len(m.New) == 0 {
		return fmt.Errorf("%w: old and new spans are required", ErrInvalidMigration)
	}
	validateSide := func(name string, spans []PartitionSpan) error {
		for i, s := range spans {
			if s.SerialLo.Cmp(s.SerialHi) >= 0 {
				return fmt.Errorf("%w: %s span %d has invalid serial range", ErrInvalidMigration, name, i)
			}
			if s.Reasons == 0 || s.Reasons&^AllStatusReasons != 0 {
				return fmt.Errorf("%w: %s span %d has invalid reason mask", ErrInvalidMigration, name, i)
			}
		}
		return nil
	}
	if err := validateSide("old", m.Old); err != nil {
		return err
	}
	if err := validateSide("new", m.New); err != nil {
		return err
	}
	if !spansEquivalent(m.Old, m.New) {
		return fmt.Errorf("%w: old and new serial/reason regions are not identical", ErrInvalidMigration)
	}
	if m.ID == "" {
		m.ID = "migration:" + digestString(m.SigningDigest())
	}
	return nil
}

// spansEquivalent reports whether two sets of serial×reason rectangles cover
// exactly the same region with no overlapping rectangles on either side.
func spansEquivalent(a, b []PartitionSpan) bool {
	type cell struct {
		lo Serial
		hi Serial
	}
	var cuts []Serial
	for _, s := range append(append([]PartitionSpan(nil), a...), b...) {
		cuts = append(cuts, s.SerialLo, s.SerialHi)
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Cmp(cuts[j]) < 0 })
	var unique []Serial
	for _, s := range cuts {
		if len(unique) == 0 || unique[len(unique)-1].Cmp(s) != 0 {
			unique = append(unique, s)
		}
	}
	for i := 0; i+1 < len(unique); i++ {
		lo, hi := unique[i], unique[i+1]
		if lo.Cmp(hi) >= 0 {
			continue
		}
		for _, reason := range AllStatusReasons.Bits() {
			ca := spanCoverCount(a, lo, hi, reason)
			cb := spanCoverCount(b, lo, hi, reason)
			if ca > 1 || cb > 1 {
				return false
			}
			if (ca == 1) != (cb == 1) {
				return false
			}
		}
	}
	return true
}

func spanCoverCount(spans []PartitionSpan, lo, hi Serial, reason ReasonCode) int {
	count := 0
	for _, s := range spans {
		if !s.Reasons.Has(reason) {
			continue
		}
		if s.SerialLo.Cmp(hi) < 0 && lo.Cmp(s.SerialHi) < 0 {
			count++
		}
	}
	return count
}

// Clone returns a deep copy.
func (m *Manifest) Clone() *Manifest { return m.clone() }

// Clone returns a deep copy.
func (m *Migration) Clone() *Migration { return m.clone() }
