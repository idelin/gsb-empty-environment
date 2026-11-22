package fedpolicy

// Effect is the decision attached to an ordered old-side rule.
type Effect bool

const (
	// Deny is the default effect and matches the target host's posture:
	// anything not explicitly allowed is refused.
	Deny Effect = false
	// Allow is the positive effect. Only all-allow projections may cross.
	Allow Effect = true
)

// String renders an effect for traces and reports.
func (e Effect) String() string {
	if e == Allow {
		return "allow"
	}
	return "deny"
}

// OrderedRule is one entry of the old-side, last-match-wins prefix policy.
// Rules are evaluated in slice order; the decision of the final rule whose
// prefix matches a principal is the principal's verdict.
type OrderedRule struct {
	Prefix NamePrefix
	Effect Effect
}

// OldPolicy is an ordered list of prefix rules with an explicit default.
// A typical deployment passes Default = Deny, which is also the target
// host's implicit posture.
type OldPolicy struct {
	// Rules are evaluated in order; later matching rules override earlier
	// ones ("last hit wins").
	Rules []OrderedRule

	// Default is returned when no rule matches. It is almost always Deny.
	Default Effect
}

// MatchedRule returns the index of the last rule whose prefix matches n and
// its effect, or -1 and the policy default when no rule matches.
func (p OldPolicy) MatchedRule(n Name) (int, Effect) {
	hit := -1
	effect := p.Default
	for i, r := range p.Rules {
		if r.Prefix.Matches(n) {
			hit = i
			effect = r.Effect
		}
	}
	return hit, effect
}

// Allows reports whether the old host would admit n under last-match-wins.
func (p OldPolicy) Allows(n Name) bool {
	_, effect := p.MatchedRule(n)
	return effect == Allow
}

// MaxPrefixLen is the longest path prefix referenced by any rule. Beyond
// this depth the policy state is stable, which keeps the product analysis
// finite even though paths are unbounded.
func (p OldPolicy) MaxPrefixLen() int {
	m := 0
	for _, r := range p.Rules {
		if len(r.Prefix.Path) > m {
			m = len(r.Prefix.Path)
		}
	}
	return m
}

// Labeler assigns the finite label set of an old principal. Implementations
// used for migration analysis must additionally implement FinitaryLabeler so
// the product search has a finite state space; the two implementations in
// this package (prefix-derived and static) both do.
type Labeler interface {
	// Labels returns the finite label set carried by n. The returned slice
	// is owned by the labeler and must not be mutated by callers.
	Labels(n Name) []Label
}

// FinitaryLabeler is a Labeler whose output depends only on a bounded prefix
// of the path. StateKey returns an opaque key capturing exactly that
// dependency; two names with the same header and an equal key carry equal
// label sets. A finite key space makes the transducer product a finite
// automaton, which is what makes unbounded-path analysis decidable here.
type FinitaryLabeler interface {
	Labeler

	// StateKey is the bounded label state for the given name. Keys are
	// compared with == (use comparable types such as strings or integers).
	StateKey(n Name) any

	// MaxLookahead is the maximum path depth the key depends on.
	MaxLookahead() int
}

// LabelRule contributes one label to every principal under a prefix. The
// PrefixLabeler unions the labels contributed by all matching rules; labels
// are therefore prefix-closed facts about principals.
type LabelRule struct {
	Prefix NamePrefix
	Label  Label
}

// PrefixLabeler derives finite label sets from ordered prefix label rules.
// It is finitary: the label state of a name depends only on the path up to
// the longest label-rule prefix.
type PrefixLabeler struct {
	Rules []LabelRule
}

var _ FinitaryLabeler = PrefixLabeler{}

// Labels returns the union of labels contributed by all matching rules.
func (l PrefixLabeler) Labels(n Name) []Label {
	set := labelSet{}
	for _, r := range l.Rules {
		if r.Prefix.Matches(n) {
			set[r.Label] = struct{}{}
		}
	}
	return set.slice()
}

// MaxLookahead is the longest prefix any label rule inspects.
func (l PrefixLabeler) MaxLookahead() int {
	m := 0
	for _, r := range l.Rules {
		if len(r.Prefix.Path) > m {
			m = len(r.Prefix.Path)
		}
	}
	return m
}

// StateKey encodes the set of matching label rules. Because whether a rule
// matches can only change within the first MaxLookahead segments, this key
// is stable for all sufficiently long extensions of a path.
func (l PrefixLabeler) StateKey(n Name) any {
	matched := make([]bool, len(l.Rules))
	for i, r := range l.Rules {
		matched[i] = r.Prefix.Matches(n)
	}
	return boolSliceKey(matched)
}

// StaticLabeler assigns one fixed label set to an entire authority and the
// empty set to everything else. It models directories where labels are
// provisioned per tenant rather than per path.
type StaticLabeler struct {
	Header HeaderKey
	Tags   []Label

	tags labelSet
}

var _ FinitaryLabeler = (*StaticLabeler)(nil)

// NewStaticLabeler builds a static labeler and precomputes its tag set.
func NewStaticLabeler(header HeaderKey, tags []Label) *StaticLabeler {
	return &StaticLabeler{Header: header, Tags: tags, tags: newLabelSet(tags)}
}

// Labels returns the fixed tags for the configured authority, else nothing.
func (l *StaticLabeler) Labels(n Name) []Label {
	if n.Header() == l.Header {
		return l.tags.slice()
	}
	return nil
}

// MaxLookahead is zero because static labels do not inspect the path.
func (l *StaticLabeler) MaxLookahead() int { return 0 }

// StateKey distinguishes only "inside the configured authority" from not.
func (l *StaticLabeler) StateKey(n Name) any {
	return n.Header() == l.Header
}

// boolSliceKey packs a boolean slice into a compact string key. It is used
// for finitary labeler states where the relevant fact is which bounded rules
// match.
func boolSliceKey(bits []bool) string {
	out := make([]byte, len(bits))
	for i, b := range bits {
		if b {
			out[i] = '1'
		} else {
			out[i] = '0'
		}
	}
	return string(out)
}
