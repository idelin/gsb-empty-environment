package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ConflictReason classifies a rule overlap rejected by Compile.
type ConflictReason int

const (
	// ReasonTargetMismatch: overlapping active rules redirect to different
	// hosts or pin different certificate fingerprints.
	ReasonTargetMismatch ConflictReason = iota + 1

	// ReasonRevokedAndApproved: an explicit revocation overlaps an approval.
	ReasonRevokedAndApproved
)

func (r ConflictReason) String() string {
	switch r {
	case ReasonTargetMismatch:
		return "overlapping rules point to different targets"
	case ReasonRevokedAndApproved:
		return "an approval overlaps an explicit revocation"
	default:
		return "unknown conflict"
	}
}

// Conflict pinpoints one concrete (source, module, version) cell where the
// rules disagree. The version is always a real version taken from the finite
// version domain, so reports name a specific conflicting version rather than a
// range.
type Conflict struct {
	Reason     ConflictReason
	OldSource  SourceIdentity
	ModulePath string
	Version    string
	RuleIDs    []string
}

// ConflictError is returned by Compile when the fixed snapshot would be
// ambiguous. It fails the whole build: a conflicting policy never resolves a
// single request.
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	lines := make([]string, 0, len(e.Conflicts))
	for _, c := range e.Conflicts {
		src := c.OldSource.canonical()
		origin := src.Host
		if src.InstanceID != "" {
			origin += "(" + src.InstanceID + ")"
		}
		lines = append(lines, fmt.Sprintf(
			"%s %s@%s: %s [rules: %s]",
			origin, c.ModulePath, c.Version, c.Reason, strings.Join(c.RuleIDs, ", "),
		))
	}
	return "netsec: migration rule conflicts: " + strings.Join(lines, "; ")
}

// Is supports errors.Is(err, &ConflictError{}) without relying on message
// text.
func (e *ConflictError) Is(target error) bool {
	_, ok := target.(*ConflictError)
	return ok
}

type ruleView struct {
	index int
	rule  Rule
	label string
}

type cellKey struct {
	host       string
	instanceID string
	module     string
	version    string
}

type cell struct {
	target         Target
	activeRuleIDs  []string
	revokedRuleIDs []string
}

// Snapshot is an immutable, compiled rule set bound to one fixed rule
// snapshot. All resolution happens against the snapshot taken at compile time;
// later mutation of the rules or domain passed to Compile has no effect.
type Snapshot struct {
	id     string
	domain map[string]map[string]struct{}
	cells  map[cellKey]*cell
}

// ID returns the snapshot identifier supplied to Compile.
func (s *Snapshot) ID() string { return s.id }

// Compile validates rules and checks them against every concrete version in
// the finite version domain. Overlapping rules must converge on one target;
// any disagreement is reported with the specific conflicting versions. The
// returned snapshot is independent of the inputs' backing memory.
func Compile(snapshotID string, rules []Rule, domain *VersionDomain) (*Snapshot, error) {
	if domain == nil || domain.modules == nil {
		return nil, errors.New("netsec: a finite version domain is required")
	}

	views := make([]ruleView, len(rules))
	seenIDs := map[string]bool{}
	var errs []error
	for i, r := range rules {
		label := strings.TrimSpace(r.ID)
		if label == "" {
			label = fmt.Sprintf("rule#%d", i)
		}
		if seenIDs[label] {
			errs = append(errs, fmt.Errorf("netsec: duplicate rule ID %q", label))
		}
		seenIDs[label] = true
		if err := validateRule(r, label); err != nil {
			errs = append(errs, err)
		}
		views[i] = ruleView{index: i, rule: r, label: label}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	sortedDomain := domain.sortedCopy()
	snap := &Snapshot{
		id:     strings.TrimSpace(snapshotID),
		domain: map[string]map[string]struct{}{},
		cells:  map[cellKey]*cell{},
	}
	for path, vs := range sortedDomain {
		known := make(map[string]struct{}, len(vs))
		for _, v := range vs {
			known[v.String()] = struct{}{}
		}
		snap.domain[path] = known
	}

	hits := make([]int, len(views))
	for modulePath, versions := range sortedDomain {
		for _, v := range versions {
			versionText := v.String()
			for _, rv := range views {
				r := rv.rule
				if !r.Scope.contains(modulePath) || !r.Versions.Contains(v) {
					continue
				}
				src := r.OldSource.canonical()
				key := cellKey{
					host:       src.Host,
					instanceID: src.InstanceID,
					module:     modulePath,
					version:    versionText,
				}
				c := snap.cells[key]
				if c == nil {
					c = &cell{}
					snap.cells[key] = c
				}
				hits[rv.index]++
				if r.Revoked {
					c.revokedRuleIDs = append(c.revokedRuleIDs, rv.label)
				} else {
					c.activeRuleIDs = append(c.activeRuleIDs, rv.label)
					if len(c.activeRuleIDs) == 1 {
						c.target = r.Target.canonical()
					}
				}
			}
		}
	}

	var conflicts []Conflict
	keys := make([]cellKey, 0, len(snap.cells))
	for key := range snap.cells {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.host != b.host {
			return a.host < b.host
		}
		if a.module != b.module {
			return a.module < b.module
		}
		return a.version < b.version
	})

	viewByLabel := map[string]ruleView{}
	for _, rv := range views {
		viewByLabel[rv.label] = rv
	}

	for _, key := range keys {
		c := snap.cells[key]
		src := SourceIdentity{Host: key.host, InstanceID: key.instanceID}
		switch {
		case len(c.revokedRuleIDs) > 0 && len(c.activeRuleIDs) > 0:
			conflicts = append(conflicts, Conflict{
				Reason:     ReasonRevokedAndApproved,
				OldSource:  src,
				ModulePath: key.module,
				Version:    key.version,
				RuleIDs:    append(append([]string(nil), c.activeRuleIDs...), c.revokedRuleIDs...),
			})
		case len(c.activeRuleIDs) > 1:
			first := viewByLabel[c.activeRuleIDs[0]].rule.Target.canonical()
			for _, label := range c.activeRuleIDs[1:] {
				other := viewByLabel[label].rule.Target.canonical()
				if !first.equal(other) {
					conflicts = append(conflicts, Conflict{
						Reason:     ReasonTargetMismatch,
						OldSource:  src,
						ModulePath: key.module,
						Version:    key.version,
						RuleIDs:    append([]string(nil), c.activeRuleIDs...),
					})
					break
				}
			}
		}
	}

	for i, rv := range views {
		if hits[i] == 0 {
			errs = append(errs, fmt.Errorf(
				"netsec: rule %q matches no version inside the approved domain", rv.label))
		}
	}

	if len(conflicts) > 0 {
		errs = append(errs, &ConflictError{Conflicts: conflicts})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return snap, nil
}

func validateRule(r Rule, label string) error {
	src := r.OldSource.canonical()
	if src.Host == "" {
		return fmt.Errorf("netsec: rule %q must bind an old source host", label)
	}
	scope := r.Scope.canonical()
	if scope.Prefix == "" {
		return fmt.Errorf("netsec: rule %q must bind a package scope", label)
	}
	if r.Versions.empty() {
		return fmt.Errorf("netsec: rule %q has an empty version interval", label)
	}
	target := r.Target.canonical()
	if r.Revoked {
		if target.NewHost != "" || len(target.Fingerprint) != 0 {
			return fmt.Errorf("netsec: revoked rule %q must not carry a target", label)
		}
		return nil
	}
	if target.NewHost == "" {
		return fmt.Errorf("netsec: rule %q must name a new source host", label)
	}
	if len(target.Fingerprint) != 32 {
		return fmt.Errorf("netsec: rule %q must pin a 32-byte certificate fingerprint", label)
	}
	return nil
}

// Resolve looks up a pre-parsed version in the fixed snapshot. It never falls
// back to a public source: the only success outcome is the pinned private
// target, and every failure returns one of the documented sentinel errors.
func (s *Snapshot) Resolve(oldSource SourceIdentity, modulePath string, version SemVer) (Target, error) {
	return s.lookup(oldSource, modulePath, version.String())
}

// ResolveModule parses version and then behaves like Resolve.
func (s *Snapshot) ResolveModule(oldSource SourceIdentity, modulePath, version string) (Target, error) {
	v, err := ParseSemVer(version)
	if err != nil {
		return Target{}, err
	}
	return s.lookup(oldSource, modulePath, v.String())
}

func (s *Snapshot) lookup(oldSource SourceIdentity, modulePath, version string) (Target, error) {
	knownVersions, ok := s.domain[modulePath]
	if !ok {
		return Target{}, fmt.Errorf("%w: %s", ErrUnknownModule, modulePath)
	}
	if _, ok := knownVersions[version]; !ok {
		return Target{}, fmt.Errorf("%w: %s@%s", ErrUnknownVersion, modulePath, version)
	}
	src := oldSource.canonical()
	c := s.cells[cellKey{
		host:       src.Host,
		instanceID: src.InstanceID,
		module:     modulePath,
		version:    version,
	}]
	if c == nil {
		return Target{}, fmt.Errorf("%w: %s from %s@%s", ErrNoRule, modulePath, src.Host, version)
	}
	if len(c.revokedRuleIDs) > 0 {
		return Target{}, fmt.Errorf("%w: %s@%s (rules: %s)",
			ErrRevoked, modulePath, version, strings.Join(c.revokedRuleIDs, ", "))
	}
	target := Target{
		NewHost:     c.target.NewHost,
		Fingerprint: Fingerprint(append([]byte(nil), c.target.Fingerprint...)),
	}
	return target, nil
}
