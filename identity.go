package evidence

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// NormalizeDomain canonicalizes a management-domain identifier.  Domains are
// compared case-insensitively.
func NormalizeDomain(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), ".")))
}

// NormalizeRouteOrigin canonicalizes a route-origin identifier.
func NormalizeRouteOrigin(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// Attribution is one authenticated identity attribution available to a probe.
// ValidFrom and ValidUntil are optional half-open bounds; a zero bound means
// unbounded in that direction.
type Attribution struct {
	ID          string    `json:"id"`
	ProbeID     string    `json:"probe_id"`
	Domain      string    `json:"domain"`
	RouteOrigin string    `json:"route_origin"`
	ValidFrom   time.Time `json:"valid_from"`
	ValidUntil  time.Time `json:"valid_until"`
	RouteProof  Digest    `json:"route_proof,omitempty"`
	Proof       Digest    `json:"proof,omitempty"`
}

// Validate checks an attribution's identifiers and validity bounds.
func (a Attribution) Validate() error {
	if !validIdentifier(a.ID) {
		return fmt.Errorf("%w: attribution id %q", ErrInvalidWitness, a.ID)
	}
	if !validIdentifier(a.ProbeID) {
		return fmt.Errorf("%w: attribution probe id %q", ErrInvalidWitness, a.ProbeID)
	}
	if NormalizeDomain(a.Domain) == "" {
		return fmt.Errorf("%w: attribution %q has empty domain", ErrInvalidWitness, a.ID)
	}
	if NormalizeRouteOrigin(a.RouteOrigin) == "" {
		return fmt.Errorf("%w: attribution %q has empty route origin", ErrInvalidWitness, a.ID)
	}
	if !a.ValidFrom.IsZero() && !a.ValidUntil.IsZero() && !a.ValidUntil.After(a.ValidFrom) {
		return fmt.Errorf("%w: attribution %q has empty validity", ErrInvalidWitness, a.ID)
	}
	return nil
}

// Canonical returns a copy with normalized domain and route origin.
func (a Attribution) Canonical() Attribution {
	a.ID = strings.TrimSpace(a.ID)
	a.ProbeID = strings.TrimSpace(a.ProbeID)
	a.Domain = NormalizeDomain(a.Domain)
	a.RouteOrigin = NormalizeRouteOrigin(a.RouteOrigin)
	return a
}

// Covers reports whether t is inside the attribution's validity interval.
func (a Attribution) Covers(t time.Time) bool {
	if !a.ValidFrom.IsZero() && t.Before(a.ValidFrom) {
		return false
	}
	if !a.ValidUntil.IsZero() && !t.Before(a.ValidUntil) {
		return false
	}
	return true
}

// ValidAt is an alias for Covers.
func (a Attribution) ValidAt(t time.Time) bool { return a.Covers(t) }

// ValiditySpan expresses the attribution's validity as a TimeSpan, or
// unbounded if both endpoints are zero.  A single finite endpoint is clamped
// against the supplied base span by Clamp.
func (a Attribution) clamp(s TimeSpan) TimeSpan {
	return s.Clamp(a.ValidFrom, a.ValidUntil)
}

// IdentityList is a versioned list of authenticated attributions.  When
// Digest is non-zero it must match ContentDigest.
type IdentityList struct {
	Version   string        `json:"version"`
	IssuedAt  time.Time     `json:"issued_at,omitempty"`
	Entries   []Attribution `json:"entries"`
	Digest    Digest        `json:"digest,omitempty"`
	Signature []byte        `json:"signature,omitempty"`
}

// Validate checks the list version and entries.
func (l IdentityList) Validate() error {
	if strings.TrimSpace(l.Version) == "" || strings.TrimSpace(l.Version) != l.Version {
		return fmt.Errorf("%w: identity list version %q", ErrInvalidWitness, l.Version)
	}
	seen := make(map[string]Attribution, len(l.Entries))
	for _, entry := range l.Entries {
		e := entry.Canonical()
		if err := e.Validate(); err != nil {
			return err
		}
		if _, ok := seen[e.ID]; ok {
			return fmt.Errorf("%w: attribution %q appears twice", ErrIdentityConflict, e.ID)
		}
		seen[e.ID] = e
	}
	if !l.Digest.IsZero() && l.Digest != l.ContentDigest() {
		return fmt.Errorf("%w: identity list %q digest mismatch", ErrIdentityConflict, l.Version)
	}
	return nil
}

// ContentDigest computes a canonical digest of the version and entries.
func (l IdentityList) ContentDigest() Digest {
	entries := make([]Attribution, len(l.Entries))
	for i, e := range l.Entries {
		entries[i] = e.Canonical()
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ID != entries[j].ID {
			return entries[i].ID < entries[j].ID
		}
		return attributionKey(entries[i]) < attributionKey(entries[j])
	})
	c := newCanonical("ip-cert-identity-list/v1")
	c.str(strings.TrimSpace(l.Version))
	c.u64(uint64(len(entries)))
	for _, e := range entries {
		c.str(e.ID)
		c.str(e.ProbeID)
		c.str(e.Domain)
		c.str(e.RouteOrigin)
		c.timeValue(e.ValidFrom)
		c.timeValue(e.ValidUntil)
		c.digestMaybe(e.RouteProof)
		c.digestMaybe(e.Proof)
	}
	return c.sum()
}

// IdentitySupplement supplies a missing attribution.  If WitnessID is
// non-empty the supplement applies only to that receipt; otherwise it applies
// to every receipt using Version.  An empty Version means the witness's own
// version.  If SelectAttributionID is set, it pins the receipt's choice (and
// is useful when several attributions are otherwise valid).
type IdentitySupplement struct {
	WitnessID           string      `json:"witness_id,omitempty"`
	Version             string      `json:"version,omitempty"`
	Attribution         Attribution `json:"attribution"`
	SelectAttributionID string      `json:"select_attribution_id,omitempty"`
}

func selectionOnly(a Attribution) bool {
	return a.ProbeID == "" && a.Domain == "" && a.RouteOrigin == "" &&
		a.ValidFrom.IsZero() && a.ValidUntil.IsZero() &&
		a.RouteProof.IsZero() && a.Proof.IsZero()
}

func (s IdentitySupplement) canonical(version string) (IdentitySupplement, error) {
	s.WitnessID = strings.TrimSpace(s.WitnessID)
	s.Version = strings.TrimSpace(s.Version)
	if s.Version == "" {
		s.Version = version
	}
	s.SelectAttributionID = strings.TrimSpace(s.SelectAttributionID)
	a := s.Attribution.Canonical()
	if a.ID == "" && s.SelectAttributionID != "" {
		a.ID = s.SelectAttributionID
	}
	s.Attribution = a
	if selectionOnly(a) {
		if s.SelectAttributionID == "" {
			return IdentitySupplement{}, fmt.Errorf("%w: empty supplement has no selection id",
				ErrMissingIdentity)
		}
		s.Attribution = Attribution{}
		if s.Version == "" {
			return IdentitySupplement{}, fmt.Errorf("%w: supplement has no identity version", ErrIdentityConflict)
		}
		return s, nil
	}
	// A per-receipt supplement may omit the attribution id; resolve fills it
	// from Witness.AttributionID before final validation.
	hadID := a.ID != ""
	if !hadID {
		a.ID = "_pending_"
	}
	if err := a.Validate(); err != nil {
		return IdentitySupplement{}, err
	}
	if !hadID {
		a.ID = ""
		s.Attribution = a
	}
	if s.Version == "" {
		return IdentitySupplement{}, fmt.Errorf("%w: supplement has no identity version", ErrIdentityConflict)
	}
	return s, nil
}

type identitySet struct {
	lists       map[string]IdentityList
	synthetic   map[string]bool
	supplements []IdentitySupplement
}

func buildIdentitySet(lists []IdentityList, supplements []IdentitySupplement) (*identitySet, error) {
	set := &identitySet{
		lists:     make(map[string]IdentityList),
		synthetic: make(map[string]bool),
	}
	for _, raw := range lists {
		if err := raw.Validate(); err != nil {
			return nil, err
		}
		version := strings.TrimSpace(raw.Version)
		canon := IdentityList{
			Version:   version,
			IssuedAt:  raw.IssuedAt,
			Entries:   append([]Attribution(nil), raw.Entries...),
			Digest:    raw.Digest,
			Signature: append([]byte(nil), raw.Signature...),
		}
		for i := range canon.Entries {
			canon.Entries[i] = canon.Entries[i].Canonical()
		}
		if prior, ok := set.lists[version]; ok {
			if !identityListsEquivalent(prior, canon) {
				return nil, fmt.Errorf("%w: version %q has different contents", ErrIdentityConflict, version)
			}
			continue
		}
		set.lists[version] = canon
	}
	for _, sup := range supplements {
		sup.WitnessID = strings.TrimSpace(sup.WitnessID)
		sup.Version = strings.TrimSpace(sup.Version)
		// Version-less supplements are resolved per witness below.
		if sup.Version != "" && sup.WitnessID == "" {
			canon, err := sup.canonical(sup.Version)
			if err != nil {
				return nil, err
			}
			if canon.Attribution.ID == "" {
				return nil, fmt.Errorf("%w: global supplement for version %q has no attribution id",
					ErrMissingIdentity, canon.Version)
			}
			if selectionOnly(canon.Attribution) {
				return nil, fmt.Errorf("%w: global supplement for version %q is selection-only",
					ErrMissingIdentity, canon.Version)
			}
			if _, ok := set.lists[canon.Version]; !ok {
				set.lists[canon.Version] = IdentityList{Version: canon.Version}
				set.synthetic[canon.Version] = true
			}
		}
		set.supplements = append(set.supplements, sup)
	}
	return set, nil
}

type resolvedAttribution struct {
	Attribution Attribution
	Domain      string
	Origin      string
}

// resolve returns the attributions available to w.  When the witness pins an
// attribution (AttributionID), exactly that attribution is returned.  When it
// does not, every matching attribution is a possible choice; the selection
// search picks exactly one per receipt.
func (s *identitySet) resolve(w Witness) ([]resolvedAttribution, error) {
	version := strings.TrimSpace(w.IdentityVersion)
	var embedded *IdentityList
	if w.IdentityList != nil {
		list := *w.IdentityList
		if err := list.Validate(); err != nil {
			return nil, err
		}
		if version == "" {
			version = strings.TrimSpace(list.Version)
		} else if strings.TrimSpace(list.Version) != version {
			return nil, fmt.Errorf("%w: witness version %q, embedded list version %q",
				ErrIdentityConflict, version, list.Version)
		}
		list.Version = version
		for i := range list.Entries {
			list.Entries[i] = list.Entries[i].Canonical()
		}
		embedded = &list
	}
	if version == "" {
		if len(s.lists) == 1 {
			for v := range s.lists {
				version = v
			}
		}
	}

	var base IdentityList
	if version != "" {
		if list, ok := s.lists[version]; ok {
			base = list
		}
		if embedded != nil {
			if base.Version == "" || s.synthetic[version] {
				base = *embedded
			} else if base.ContentDigest() != embedded.ContentDigest() {
				return nil, fmt.Errorf("%w: embedded identity list version %q differs from catalog",
					ErrIdentityConflict, version)
			}
		}
		if base.Version == "" && s.synthetic[version] {
			base = s.lists[version]
		}
		if base.Version == "" {
			// An otherwise missing version may be supplied, in whole or in
			// part, by supplements.
			base = IdentityList{Version: version}
		}
	} else if embedded != nil {
		version = embedded.Version
		base = *embedded
	} else {
		// If the catalog has exactly one version we may infer it; otherwise the
		// matching supplements must identify exactly one version.
		versions := make(map[string]struct{})
		for _, sup := range s.supplements {
			if sup.WitnessID != "" && sup.WitnessID != w.ID {
				continue
			}
			if strings.TrimSpace(sup.Version) != "" {
				versions[strings.TrimSpace(sup.Version)] = struct{}{}
			}
		}
		if len(versions) == 1 {
			for v := range versions {
				version = v
				base = s.lists[version]
			}
		} else if len(versions) > 1 {
			return nil, fmt.Errorf("%w: receipt %q matches several identity versions",
				ErrAmbiguousIdentity, w.ID)
		}
		if version == "" {
			return nil, fmt.Errorf("%w: receipt %q has no identity list/version", ErrMissingIdentity, w.ID)
		}
	}

	base.Entries = append([]Attribution(nil), base.Entries...)
	for i := range base.Entries {
		base.Entries[i] = base.Entries[i].Canonical()
	}
	if !w.IdentityListDigest.IsZero() && w.IdentityListDigest != base.ContentDigest() {
		return nil, fmt.Errorf("%w: receipt %q identity list digest mismatch",
			ErrIdentityConflict, w.ID)
	}

	// Apply global and per-witness supplements.
	for _, raw := range s.supplements {
		if raw.WitnessID != "" && raw.WitnessID != w.ID {
			continue
		}
		sup, err := raw.canonical(version)
		if err != nil {
			return nil, err
		}
		if sup.WitnessID != "" && sup.WitnessID != w.ID {
			continue
		}
		if sup.Version != version && raw.Version != "" {
			continue
		}
		if selectionOnly(sup.Attribution) {
			continue
		}
		if sup.Attribution.ProbeID == "" {
			sup.Attribution.ProbeID = w.ProbeID
		}
		if sup.Attribution.ID == "" {
			sup.Attribution.ID = strings.TrimSpace(w.AttributionID)
		}
		if err := sup.Attribution.Validate(); err != nil {
			return nil, err
		}
		var mergeErr error
		base.Entries, mergeErr = mergeAttribution(base.Entries, sup.Attribution)
		if mergeErr != nil {
			return nil, mergeErr
		}
	}

	claimedDomain := NormalizeDomain(w.Domain)
	claimedOrigin := NormalizeRouteOrigin(w.RouteOrigin)
	selectedID := strings.TrimSpace(w.AttributionID)
	var forcedID string
	for _, raw := range s.supplements {
		if raw.WitnessID != "" && raw.WitnessID != w.ID {
			continue
		}
		if id := strings.TrimSpace(raw.SelectAttributionID); id != "" {
			if raw.Version == "" || strings.TrimSpace(raw.Version) == version {
				forcedID = id
			}
		}
	}
	if forcedID != "" {
		if selectedID != "" && selectedID != forcedID {
			return nil, fmt.Errorf("%w: receipt %q selects %q but supplement pins %q",
				ErrAmbiguousIdentity, w.ID, selectedID, forcedID)
		}
		selectedID = forcedID
	}

	options := make([]resolvedAttribution, 0, len(base.Entries))
	for _, entry := range base.Entries {
		entry = entry.Canonical()
		if entry.ProbeID != w.ProbeID {
			continue
		}
		if selectedID != "" && entry.ID != selectedID {
			continue
		}
		if claimedDomain != "" && entry.Domain != claimedDomain {
			continue
		}
		if claimedOrigin != "" && entry.RouteOrigin != claimedOrigin {
			continue
		}
		if !w.IdentityProof.IsZero() && !entry.Proof.IsZero() && w.IdentityProof != entry.Proof {
			continue
		}
		if !w.RouteProof.IsZero() && !entry.RouteProof.IsZero() && w.RouteProof != entry.RouteProof {
			continue
		}
		options = append(options, resolvedAttribution{
			Attribution: entry,
			Domain:      entry.Domain,
			Origin:      entry.RouteOrigin,
		})
	}
	if len(options) == 0 {
		if selectedID != "" {
			return nil, fmt.Errorf("%w: receipt %q selects unknown attribution %q (version %q)",
				ErrMissingIdentity, w.ID, selectedID, version)
		}
		return nil, fmt.Errorf("%w: receipt %q has no matching attribution (version %q)",
			ErrMissingIdentity, w.ID, version)
	}
	if selectedID != "" && len(options) > 1 {
		return nil, fmt.Errorf("%w: receipt %q selected attribution %q is ambiguous",
			ErrAmbiguousIdentity, w.ID, selectedID)
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Attribution.ID != options[j].Attribution.ID {
			return options[i].Attribution.ID < options[j].Attribution.ID
		}
		return attributionKey(options[i].Attribution) < attributionKey(options[j].Attribution)
	})
	return options, nil
}

func mergeAttribution(entries []Attribution, entry Attribution) ([]Attribution, error) {
	entry = entry.Canonical()
	for i := range entries {
		if entries[i].ID != entry.ID {
			continue
		}
		if attributionsEqual(entries[i].Canonical(), entry) {
			return entries, nil
		}
		return nil, fmt.Errorf("%w: supplement for attribution %q conflicts with an existing definition",
			ErrIdentityConflict, entry.ID)
	}
	return append(entries, entry), nil
}

func attributionsEqual(a, b Attribution) bool {
	a = a.Canonical()
	b = b.Canonical()
	return a.ID == b.ID &&
		a.ProbeID == b.ProbeID &&
		a.Domain == b.Domain &&
		a.RouteOrigin == b.RouteOrigin &&
		a.ValidFrom.Equal(b.ValidFrom) &&
		a.ValidUntil.Equal(b.ValidUntil) &&
		a.RouteProof == b.RouteProof &&
		a.Proof == b.Proof
}

func attributionKey(a Attribution) string {
	a = a.Canonical()
	return a.ID + "\x00" + a.ProbeID + "\x00" + a.Domain + "\x00" + a.RouteOrigin +
		"\x00" + a.ValidFrom.UTC().Format(time.RFC3339Nano) +
		"\x00" + a.ValidUntil.UTC().Format(time.RFC3339Nano) +
		"\x00" + a.RouteProof.Hex() + "\x00" + a.Proof.Hex()
}

func identityListsEquivalent(a, b IdentityList) bool {
	if strings.TrimSpace(a.Version) != strings.TrimSpace(b.Version) {
		return false
	}
	return a.ContentDigest() == b.ContentDigest()
}

func validIdentifier(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
