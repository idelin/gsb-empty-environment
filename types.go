// Package projection synthesises and certifies public-label projections for
// finite probabilistic protocols.
//
// A protocol is a finite state machine whose transitions have exact rational
// weights and visible labels.  Several initial states are treated as competing
// secret hypotheses.  A projection merges visible labels, and is safe when the
// public observations of those hypotheses are indistinguishable until a
// correctly signed release event deliberately publishes a secret category.
//
// All probability arithmetic in this package is exact.  No sampling is used.
package projection

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Label is an original, public event label.
type Label string

// StateID identifies a protocol state.
type StateID string

// SecretID identifies one possible secret initial state.
type SecretID string

// Category is the secret category carried by a release event.
type Category string

// MergePair is a symmetric pair of labels.  JSON encodes it as ["A","B"].
type MergePair [2]Label

// Pair constructs a merge constraint.
func Pair(a, b Label) MergePair { return MergePair{a, b} }

// Release describes an event that may publish a secret category.  A release is
// observable only when the host accepts its signature.  Signed releases are
// allowed to distinguish secret categories; an unsigned or rejected release is
// treated as an ordinary event and its Category is ignored.
type Release struct {
	Category  Category `json:"category"`
	Signed    bool     `json:"signed"`
	Signature string   `json:"signature,omitempty"`
}

// Transition is one probabilistic edge.  Probability may be a fraction such
// as "1/3", an integer, or an exact decimal such as "0.125".  Weight, when
// non-nil, takes precedence and allows callers to avoid string conversion.
type Transition struct {
	From        StateID  `json:"from"`
	To          StateID  `json:"to"`
	Label       Label    `json:"label"`
	Probability string   `json:"probability"`
	Weight      *big.Rat `json:"-"`
	Release     *Release `json:"release,omitempty"`
}

// Protocol is the finite probabilistic protocol supplied to the registry.
//
// States is optional: states mentioned by Initial or Transitions are collected
// automatically.  Initial maps each secret hypothesis to its initial state.
// The probability of all transitions leaving a state must be at most one; the
// remaining mass is the probability of early termination, which is observable.
//
// VerifyRelease is the host signature verifier.  When nil, Signed on the
// release is the host attestation, or a non-empty Signature is accepted.
type Protocol struct {
	States        []StateID            `json:"states,omitempty"`
	Initial       map[SecretID]StateID `json:"initial"`
	Transitions   []Transition         `json:"transitions"`
	VerifyRelease func(Release) bool   `json:"-"`
}

// Config describes which original labels may be merged and which business
// labels must remain distinguishable.  AllowedMerges are hints: the component
// takes their symmetric transitive closure.  A final block must nevertheless
// be a clique in that closure and must contain no ForbiddenMerges pair.
type Config struct {
	Labels          []Label     `json:"labels,omitempty"`
	AllowedMerges   []MergePair `json:"allowed_merges,omitempty"`
	ForbiddenMerges []MergePair `json:"forbidden_merges,omitempty"`
}

// Projection maps each original visible label to a public label.  Missing
// entries are interpreted as identity mappings.
type Projection struct {
	Mapping map[Label]Label `json:"mapping"`
}

// Public returns the projected label.  The zero-value projection is identity.
func (p Projection) Public(label Label) Label {
	if p.Mapping != nil {
		if out, ok := p.Mapping[label]; ok {
			return out
		}
	}
	return label
}

// CategoryCount counts distinct public labels used by a label universe.
func (p Projection) CategoryCount(labels []Label) int {
	seen := make(map[Label]struct{}, len(labels))
	for _, label := range labels {
		seen[p.Public(label)] = struct{}{}
	}
	return len(seen)
}

// Clone returns a deep copy of the projection.
func (p Projection) Clone() Projection {
	out := Projection{Mapping: make(map[Label]Label, len(p.Mapping))}
	for from, to := range p.Mapping {
		out.Mapping[from] = to
	}
	return out
}

// Result is the maximum-category safe projection and its certificates.
type Result struct {
	Projection    Projection  `json:"projection"`
	CategoryCount int         `json:"category_count"`
	Certificate   Certificate `json:"certificate"`
}

// Certificate contains two independently replayable parts: a probabilistic
// bisimulation proof, and an exhaustive optimality account.
type Certificate struct {
	Encoding      string                `json:"encoding"`
	CategoryCount int                   `json:"category_count"`
	Projection    Projection            `json:"projection"`
	Safety        SafetyCertificate     `json:"safety"`
	Optimality    OptimalityCertificate `json:"optimality"`
}

// Observation is one ordinary event or signed release on a witness trace.
type Observation struct {
	Kind     string   `json:"kind"`
	Label    Label    `json:"label,omitempty"`
	Category Category `json:"category,omitempty"`
}

// SafetyFailure is a concrete counterexample or a failure to couple two
// probability distributions.
type SafetyFailure struct {
	Reason       string        `json:"reason"`
	SecretA      SecretID      `json:"secret_a,omitempty"`
	SecretB      SecretID      `json:"secret_b,omitempty"`
	StateA       StateID       `json:"state_a"`
	StateB       StateID       `json:"state_b"`
	Trace        []Observation `json:"trace,omitempty"`
	Label        Label         `json:"label,omitempty"`
	Category     Category      `json:"category,omitempty"`
	ProbabilityA string        `json:"probability_a,omitempty"`
	ProbabilityB string        `json:"probability_b,omitempty"`
	Detail       string        `json:"detail,omitempty"`
}

func (f *SafetyFailure) Error() string {
	if f == nil {
		return "unsafe projection"
	}
	where := fmt.Sprintf("%s vs %s", f.StateA, f.StateB)
	if len(f.Trace) > 0 {
		where += " after public trace"
	}
	if f.Detail != "" {
		return fmt.Sprintf("unsafe projection (%s, %s): %s", f.Reason, where, f.Detail)
	}
	return fmt.Sprintf("unsafe projection (%s, %s)", f.Reason, where)
}

// CouplingCell is one positive entry of a rational transport plan.
type CouplingCell struct {
	From        StateID `json:"from"`
	To          StateID `json:"to"`
	FromLabel   Label   `json:"from_label,omitempty"`
	ToLabel     Label   `json:"to_label,omitempty"`
	Probability string  `json:"probability"`
}

// OrdinaryProof proves equality and coupling for one ordinary public label.
type OrdinaryProof struct {
	Label        Label          `json:"label"`
	ProbabilityA string         `json:"probability_a"`
	ProbabilityB string         `json:"probability_b"`
	Coupling     []CouplingCell `json:"coupling,omitempty"`
}

// ReleaseProof proves that, conditional on the same signed release category,
// the projected labels and successor distributions agree.
type ReleaseProof struct {
	Category     Category       `json:"category"`
	ProbabilityA string         `json:"probability_a"`
	ProbabilityB string         `json:"probability_b"`
	Coupling     []CouplingCell `json:"coupling,omitempty"`
}

// PairProof is the coinductive step for one ordered state pair.
type PairProof struct {
	A            StateID         `json:"a"`
	B            StateID         `json:"b"`
	TerminationA string          `json:"termination_a"`
	TerminationB string          `json:"termination_b"`
	Ordinary     []OrdinaryProof `json:"ordinary,omitempty"`
	Releases     []ReleaseProof  `json:"releases,omitempty"`
}

// StatePair is a member of the bisimulation relation.
type StatePair struct {
	A StateID `json:"a"`
	B StateID `json:"b"`
}

// SafetyCertificate is a finite probabilistic bisimulation containing every
// pair of secret initial states.  Each PairProof supplies the rational
// couplings needed to check its coinductive step.
type SafetyCertificate struct {
	Encoding      string      `json:"encoding"`
	CategoryCount int         `json:"category_count"`
	Labels        []Label     `json:"labels"`
	Relation      []StatePair `json:"relation"`
	Pairs         []PairProof `json:"pairs"`
}

// Refutation is one legal projection with more categories than the optimum
// that was rejected by an exact safety counterexample.
type Refutation struct {
	Encoding      string         `json:"encoding"`
	CategoryCount int            `json:"category_count"`
	Projection    Projection     `json:"projection"`
	Witness       *SafetyFailure `json:"witness"`
}

// MaxRefutationWitnesses bounds the human-readable refutations included in a
// certificate.  RefutationDigest still covers every legal candidate above the
// optimum, and VerifyCertificate replays the exhaustive search.
const MaxRefutationWitnesses = 512

// OptimalityCertificate binds the canonical optimum and an order-independent
// digest of every legal projection that retained more categories.
type OptimalityCertificate struct {
	CategoryCount     int          `json:"category_count"`
	CanonicalEncoding string       `json:"canonical_encoding"`
	AboveBest         int          `json:"above_best"`
	Refuted           int          `json:"refuted"`
	RefutationDigest  string       `json:"refutation_digest"`
	Exhaustive        bool         `json:"exhaustive"`
	Refutations       []Refutation `json:"refutations,omitempty"`
}

// ErrNoSafeProjection means every legal projection leaks information or breaks
// a business distinction.
var ErrNoSafeProjection = errors.New("projection: no safe projection")

// ParseProbability parses an exact non-negative probability.
func ParseProbability(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("projection: empty probability")
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("projection: invalid probability %q", value)
	}
	if r.Sign() < 0 {
		return nil, fmt.Errorf("projection: negative probability %q", value)
	}
	return r, nil
}

// RatString renders a rational in canonical "n/d" or integer form.
func RatString(r *big.Rat) string {
	if r == nil {
		return "0"
	}
	return r.RatString()
}

func addRat(dst, value *big.Rat) *big.Rat {
	if dst == nil {
		dst = new(big.Rat)
	}
	return new(big.Rat).Add(dst, value)
}

func cmpRat(a, b *big.Rat) int {
	if a == nil {
		a = new(big.Rat)
	}
	if b == nil {
		b = new(big.Rat)
	}
	return a.Cmp(b)
}

func sortedLabelsFromSet(set map[Label]struct{}) []Label {
	labels := make([]Label, 0, len(set))
	for label := range set {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i] < labels[j] })
	return labels
}
