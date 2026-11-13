package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Secret is one member of a finite candidate secret set.
type Secret struct {
	// ID is used in reports.  If empty, a readable value is derived from the
	// content.
	ID string

	// Content is installed in the victim page.  If nil, ID is used as content.
	Content []byte

	// Digest optionally overrides the computed victim digest.  Two secrets
	// with the same digest model a digest collision alias.
	Digest Digest

	// Tags adds victim-page sensitive tags for this candidate.
	Tags []string

	// Unshareable forces the victim page to be non-shareable.
	Unshareable bool
}

// NewSecret builds a secret from an identifier and content.
func NewSecret(id, content string) Secret {
	return Secret{ID: id, Content: []byte(content)}
}

func (s Secret) content() []byte {
	if s.Content != nil {
		return s.Content
	}
	return []byte(s.ID)
}

func (s Secret) name(index int) string {
	if s.ID != "" {
		return s.ID
	}
	if len(s.Content) != 0 {
		return string(s.Content)
	}
	return fmt.Sprintf("secret-%d", index)
}

// OpKind is an attacker operation kind.
type OpKind string

const (
	OpPrime   OpKind = "prime"
	OpWrite   OpKind = "write"
	OpRead    OpKind = "read"
	OpRelease OpKind = "release"
)

// Operation is one step in an attacker Prime/Write sequence.
type Operation struct {
	Kind    OpKind
	Content []byte
	Digest  Digest
	Data    []byte
}

// Prime creates an attacker-controlled candidate page.  The operation then
// performs a fixed-cost access, which is the "prime" observation.
func Prime(content []byte) Operation {
	return Operation{Kind: OpPrime, Content: append([]byte(nil), content...)}
}

// PrimeString is Prime for string content.
func PrimeString(content string) Operation { return Prime([]byte(content)) }

// PrimeDigest creates an attacker page with an explicit digest.  This is used
// to model an attacker that knows a colliding digest alias.
func PrimeDigest(content []byte, digest Digest) Operation {
	return Operation{
		Kind:    OpPrime,
		Content: append([]byte(nil), content...),
		Digest:  digest,
	}
}

// Write writes data to the most recently primed page.
func Write(data []byte) Operation {
	return Operation{Kind: OpWrite, Data: append([]byte(nil), data...)}
}

// WriteString is Write for string data.
func WriteString(data string) Operation { return Write([]byte(data)) }

// Read reads the most recently primed page.
func Read() Operation { return Operation{Kind: OpRead} }

// Release releases the most recently primed page.
func Release() Operation { return Operation{Kind: OpRelease} }

// Aliases with Op suffixes for callers that avoid the short names.
func OpPrimeWith(content []byte) Operation   { return Prime(content) }
func OpPrimeString(content string) Operation { return PrimeString(content) }
func OpWriteWith(data []byte) Operation      { return Write(data) }
func OpWriteString(data string) Operation    { return WriteString(data) }
func OpReadOperation() Operation             { return Read() }
func OpReleaseOperation() Operation          { return Release() }
func PrimeBytes(content []byte) Operation    { return Prime(content) }
func WriteBytes(data []byte) Operation       { return Write(data) }
func ReadOperation() Operation               { return Read() }
func ReleaseOperation() Operation            { return Release() }

// Sequence is an ordered attacker Prime/Write sequence.
type Sequence []Operation

// Len returns the number of operations.
func (s Sequence) Len() int { return len(s) }

// Clone returns an independent copy.
func (s Sequence) Clone() Sequence {
	if s == nil {
		return nil
	}
	out := make(Sequence, len(s))
	for i, op := range s {
		out[i] = op
		out[i].Content = append([]byte(nil), op.Content...)
		out[i].Data = append([]byte(nil), op.Data...)
	}
	return out
}

func (s Sequence) String() string {
	parts := make([]string, 0, len(s))
	for _, op := range s {
		switch op.Kind {
		case OpPrime:
			parts = append(parts, fmt.Sprintf("prime(%q)", string(op.Content)))
		case OpWrite:
			parts = append(parts, fmt.Sprintf("write(%q)", string(op.Data)))
		default:
			parts = append(parts, string(op.Kind))
		}
	}
	return strings.Join(parts, ",")
}

// ExperimentConfig controls a leak experiment.
type ExperimentConfig struct {
	// Policy may be a MergePolicy implementation or a callback with the shape
	// func(Page, Page) bool (or func(*Page, *Page) bool).
	Policy     any
	PolicyFunc func(incoming, resident Page) bool
	Costs      CostModel
	DigestFunc DigestFunc

	AttackerDomain DomainID
	VictimDomain   DomainID

	VictimTags   []string
	AttackerTags []string

	// VictimShareable defaults to true.  VictimUnshareable forces false.
	VictimShareable   bool
	VictimUnshareable bool

	// AttackerShareable defaults to true.  AttackerUnshareable forces false.
	AttackerShareable   bool
	AttackerUnshareable bool

	// COWFault is forwarded to the memory host.
	COWFault func(Page) error

	// Probes overrides the default generated Prime/Write probes.  Sequences
	// aliases it for callers that use that spelling.
	Probes    []Sequence
	Sequences []Sequence

	// MaxSequenceLength is retained for harness compatibility.  The complete
	// search space for this timing model is one prime followed by one write,
	// so generated probes are always length two.
	MaxSequenceLength int
}

// DefaultExperimentConfig returns the defaults used by NewRunner.
func DefaultExperimentConfig() ExperimentConfig {
	return ExperimentConfig{
		Policy:            ConservativePolicy{},
		Costs:             DefaultCostModel(),
		AttackerDomain:    "attacker",
		VictimDomain:      "victim",
		VictimShareable:   true,
		AttackerShareable: true,
		MaxSequenceLength: 2,
	}
}

func (c ExperimentConfig) normalized() ExperimentConfig {
	d := DefaultExperimentConfig()
	policy := coercePolicy(c.Policy)
	if policy == nil {
		if c.PolicyFunc != nil {
			policy = PolicyFunc(c.PolicyFunc)
		} else {
			policy = coercePolicy(d.Policy)
		}
	}
	c.Policy = policy
	if c.AttackerDomain == "" {
		c.AttackerDomain = d.AttackerDomain
	}
	if c.VictimDomain == "" {
		c.VictimDomain = d.VictimDomain
	}
	if c.Costs == (CostModel{}) {
		c.Costs = d.Costs
	}
	c.Costs = c.Costs.Normalize()
	if c.DigestFunc == nil {
		c.DigestFunc = SHA256Digest
	}
	if c.VictimUnshareable {
		c.VictimShareable = false
	} else if !c.VictimShareable {
		// The zero value means "use the safe default", which is shareable.
		c.VictimShareable = true
	}
	if c.AttackerUnshareable {
		c.AttackerShareable = false
	} else if !c.AttackerShareable {
		c.AttackerShareable = true
	}
	if c.MaxSequenceLength < 2 {
		c.MaxSequenceLength = 2
	}
	if len(c.Probes) == 0 && len(c.Sequences) != 0 {
		c.Probes = c.Sequences
	}
	return c
}

// Runner executes attacker sequences against fresh victim states.
type Runner struct {
	cfg ExperimentConfig
}

// NewRunner builds an experiment runner.
func NewRunner(cfg ExperimentConfig) *Runner {
	return &Runner{cfg: cfg.normalized()}
}

// Config returns the normalized runner configuration.
func (r *Runner) Config() ExperimentConfig { return r.cfg }

// Run executes seq in a fresh host containing one victim page for secret.
// The returned trace contains only attacker operations; victim page setup is
// not observable.
func (r *Runner) Run(secret Secret, seq Sequence) (Trace, error) {
	mem := NewMemory(Config{
		Policy:     r.cfg.Policy,
		Costs:      r.cfg.Costs,
		DigestFunc: r.cfg.DigestFunc,
		COWFault:   r.cfg.COWFault,
	})

	victim := mem.CreatePage(PageSpec{
		DomainID:  r.cfg.VictimDomain,
		Content:   secret.content(),
		Digest:    secret.Digest,
		Tags:      append(append([]string(nil), r.cfg.VictimTags...), secret.Tags...),
		Shareable: r.cfg.VictimShareable && !secret.Unshareable,
	})
	_ = victim

	var (
		active       PageRef
		havePage     bool
		observations Trace
	)
	for _, op := range seq {
		switch op.Kind {
		case OpPrime:
			ref := mem.CreatePage(PageSpec{
				DomainID:  r.cfg.AttackerDomain,
				Content:   op.Content,
				Digest:    op.Digest,
				Tags:      r.cfg.AttackerTags,
				Shareable: r.cfg.AttackerShareable,
			})
			active, havePage = ref, true
			// Prime is an access, so it has the fixed access cost.
			result, err := mem.Read(ref)
			if err != nil {
				return observations, err
			}
			observations = append(observations, result.Observation)

		case OpWrite:
			if !havePage {
				return observations, errors.New("netsec: write without a primed page")
			}
			result, err := mem.Write(active, op.Data)
			if result.Observation.Event != "" {
				observations = append(observations, result.Observation)
			}
			if err != nil {
				return observations, err
			}

		case OpRead:
			if !havePage {
				return observations, errors.New("netsec: read without a primed page")
			}
			result, err := mem.Read(active)
			if err != nil {
				return observations, err
			}
			observations = append(observations, result.Observation)

		case OpRelease:
			if havePage {
				if err := mem.Delete(active); err != nil {
					return observations, err
				}
				havePage = false
			}

		default:
			return observations, fmt.Errorf("netsec: unknown operation %q", op.Kind)
		}
	}
	return observations, nil
}

// RunContent runs a sequence against a victim page with the given content.
func (r *Runner) RunContent(content []byte, seq Sequence) (Trace, error) {
	return r.Run(Secret{Content: content}, seq)
}

func (r *Runner) probesFor(secrets []Secret) []Sequence {
	if len(r.cfg.Probes) != 0 {
		out := make([]Sequence, len(r.cfg.Probes))
		for i, probe := range r.cfg.Probes {
			out[i] = probe.Clone()
		}
		return out
	}

	// The finite candidate set itself supplies the prime alphabet.  Trying one
	// candidate followed by a write is a complete search for this model: the
	// only secret-dependent event is the first write after a possible merge.
	seen := make(map[string]struct{})
	out := make([]Sequence, 0, len(secrets))
	for _, secret := range secrets {
		content := secret.content()
		digest := secret.Digest
		if digest == "" {
			digest = DigestOf(content, r.cfg.DigestFunc)
		}
		key := string(digest) + "\x00" + string(content)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Sequence{
			PrimeDigest(content, digest),
			Write(nil),
		})
	}
	return out
}

// SecretGroup is a set of secrets that produced the same observed signature
// for every probe.
type SecretGroup struct {
	Key       string
	Secrets   []string
	Signature []Trace
}

// Certainty describes how strongly a report establishes a leak.
type Certainty string

const (
	CertaintyNone     Certainty = "none"
	CertaintyPossible Certainty = "possible"
	CertaintyDefinite Certainty = "definite"
)

// Verdict is the experiment conclusion.
type Verdict string

const (
	NoLeak       Verdict = "no-leak"
	PossibleLeak Verdict = "possible-leak"
	Leak         Verdict = "leak"
)

// LeakReport is the result of enumerating a finite secret set.
type LeakReport struct {
	Policy           string
	AttackerDomain   DomainID
	VictimDomain     DomainID
	CrossDomain      bool
	ProbeCount       int
	Groups           []SecretGroup
	ShortestSequence Sequence
	Certainty        Certainty
	// Distinguishes holds the two secret names separated by the shortest
	// sequence, when one exists.
	Distinguishes [2]string
	Verdict       Verdict
}

// Leaked reports a definite leak.
func (r *LeakReport) Leaked() bool { return r.Verdict == Leak }

// Possible reports whether a leak is possible, including definite leaks.
func (r *LeakReport) Possible() bool {
	return r.Verdict == Leak || r.Verdict == PossibleLeak
}

// ShortestDistinguishingSequence returns the shortest sequence found, or nil.
func (r *LeakReport) ShortestDistinguishingSequence() Sequence {
	return r.ShortestSequence.Clone()
}

// DistinguishingPair returns the pair separated by the shortest sequence.
func (r *LeakReport) DistinguishingPair() (string, string, bool) {
	if r.Certainty == CertaintyNone || r.ShortestSequence == nil {
		return "", "", false
	}
	return r.Distinguishes[0], r.Distinguishes[1], true
}

// CrossDomainIndependent reports whether every candidate produced the same
// observable signature.  A hardened, correctly scoped policy must make this
// true for a cross-domain experiment.
func (r *LeakReport) CrossDomainIndependent() bool {
	return len(r.Groups) <= 1
}

// Report runs every probe for every secret, groups equal observations and
// finds the shortest distinguishing Prime/Write sequence.
func (r *Runner) Report(secrets []Secret) (*LeakReport, error) {
	probes := r.probesFor(secrets)
	names := make([]string, len(secrets))
	signatures := make([][]Trace, len(secrets))
	for i, secret := range secrets {
		names[i] = secret.name(i)
		signatures[i] = make([]Trace, len(probes))
		for p, probe := range probes {
			trace, err := r.Run(secret, probe)
			if err != nil {
				return nil, fmt.Errorf("netsec: secret %q probe %d: %w", names[i], p, err)
			}
			signatures[i][p] = trace
		}
	}

	policy := coercePolicy(r.cfg.Policy)
	report := &LeakReport{
		Policy:         policy.Name(),
		AttackerDomain: r.cfg.AttackerDomain,
		VictimDomain:   r.cfg.VictimDomain,
		CrossDomain:    r.cfg.AttackerDomain != r.cfg.VictimDomain,
		ProbeCount:     len(probes),
		Certainty:      CertaintyNone,
		Verdict:        NoLeak,
	}

	// Group secrets by the exact observable cost intervals of every probe.
	groupIndex := make(map[string]int)
	for i, sig := range signatures {
		key := signatureKey(sig)
		if index, ok := groupIndex[key]; ok {
			report.Groups[index].Secrets = append(report.Groups[index].Secrets, names[i])
			continue
		}
		groupIndex[key] = len(report.Groups)
		report.Groups = append(report.Groups, SecretGroup{
			Key:       key,
			Secrets:   []string{names[i]},
			Signature: cloneSignature(sig),
		})
	}

	var (
		shortestDistinct Sequence
		distinctPair     [2]string
		shortestOverlap  Sequence
		overlapPair      [2]string
	)
	for p, probe := range probes {
		for i := 0; i < len(secrets); i++ {
			for j := i + 1; j < len(secrets); j++ {
				relation := CompareTraces(signatures[i][p], signatures[j][p])
				pair := [2]string{names[i], names[j]}
				switch relation {
				case TracesDistinct:
					if shorter(probe, shortestDistinct) {
						shortestDistinct = probe.Clone()
						distinctPair = pair
					}
				case TracesOverlap:
					if shorter(probe, shortestOverlap) {
						shortestOverlap = probe.Clone()
						overlapPair = pair
					}
				}
			}
		}
	}

	if shortestDistinct != nil {
		report.ShortestSequence = shortestDistinct
		report.Distinguishes = distinctPair
		report.Certainty = CertaintyDefinite
		report.Verdict = Leak
	} else if shortestOverlap != nil {
		report.ShortestSequence = shortestOverlap
		report.Distinguishes = overlapPair
		report.Certainty = CertaintyPossible
		report.Verdict = PossibleLeak
	}
	return report, nil
}

func shorter(candidate, current Sequence) bool {
	if current == nil {
		return true
	}
	if len(candidate) != len(current) {
		return len(candidate) < len(current)
	}
	return candidate.String() < current.String()
}

func signatureKey(signature []Trace) string {
	parts := make([]string, len(signature))
	for i, trace := range signature {
		parts[i] = trace.CostKey()
	}
	return strings.Join(parts, "\x01")
}

func cloneSignature(signature []Trace) []Trace {
	out := make([]Trace, len(signature))
	for i, trace := range signature {
		out[i] = append(Trace(nil), trace...)
	}
	return out
}

// RunExperiment enumerates a finite candidate set.  It accepts []string or
// []Secret.  A string candidate uses the same bytes for its ID and content.
// The optional argument may be an ExperimentConfig, a MergePolicy, a policy
// callback, or nil (defaults).
func RunExperiment[T string | Secret](secrets []T, configs ...any) (*LeakReport, error) {
	var config any
	if len(configs) > 0 {
		config = configs[0]
	}
	cfg, err := experimentConfigFrom(config)
	if err != nil {
		return nil, err
	}
	converted := make([]Secret, len(secrets))
	for i, value := range secrets {
		switch typed := any(value).(type) {
		case string:
			converted[i] = Secret{ID: typed, Content: []byte(typed)}
		case Secret:
			converted[i] = typed
		}
	}
	return NewRunner(cfg).Report(converted)
}

func experimentConfigFrom(value any) (ExperimentConfig, error) {
	switch typed := value.(type) {
	case nil:
		return DefaultExperimentConfig(), nil
	case ExperimentConfig:
		return typed, nil
	case MergePolicy:
		cfg := DefaultExperimentConfig()
		cfg.Policy = typed
		return cfg, nil
	case func(incoming, resident Page) bool:
		cfg := DefaultExperimentConfig()
		cfg.Policy = PolicyFunc(typed)
		return cfg, nil
	default:
		return ExperimentConfig{}, fmt.Errorf("netsec: unsupported experiment configuration %T", value)
	}
}

// RunStringsExperiment is RunExperiment for []string.
func RunStringsExperiment(secrets []string, configs ...any) (*LeakReport, error) {
	return RunExperiment(secrets, configs...)
}

// ProbeLeak is an alias for RunStringsExperiment.
func ProbeLeak(secrets []string, configs ...any) (*LeakReport, error) {
	return RunStringsExperiment(secrets, configs...)
}

// RunExperimentWithPolicy is the common experiment with an explicit policy.
func RunExperimentWithPolicy(secrets []string, policy any) (*LeakReport, error) {
	return RunStringsExperiment(secrets, policy)
}

// ShortestDistinguishingSequence runs a report and returns its shortest
// sequence that separates any two candidate secrets.  It accepts []string or
// []Secret and the same optional configuration values as RunExperiment.
func ShortestDistinguishingSequence[T string | Secret](secrets []T, configs ...any) (Sequence, bool, error) {
	report, err := RunExperiment(secrets, configs...)
	if err != nil {
		return nil, false, err
	}
	if report.Certainty == CertaintyNone {
		return nil, false, nil
	}
	return report.ShortestSequence.Clone(), true, nil
}

// ShortestStringDistinguishingSequence is the []string convenience form.
func ShortestStringDistinguishingSequence(secrets []string, configs ...any) (Sequence, bool, error) {
	report, err := RunStringsExperiment(secrets, configs...)
	if err != nil {
		return nil, false, err
	}
	if report.Certainty == CertaintyNone {
		return nil, false, nil
	}
	return report.ShortestSequence.Clone(), true, nil
}
