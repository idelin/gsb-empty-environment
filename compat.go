package symexec

// This file keeps small compatibility aliases close together. They let a
// verification host use terminology from either protocol-analysis or
// symbolic-execution APIs without changing the core implementation.

// Status aliases.
const (
	StatusUnknown   = Unknown
	StatusAttack    = Attack
	StatusSafe      = Safe
	AttackFound     = Attack
	NoAttack        = Safe
	BudgetExhausted = Unknown

	SendStep    = StepSend
	ReceiveStep = StepReceive
	FreshStep   = StepFresh
	BeginStep   = StepBegin
	EndStep     = StepEnd
	ActionSend  = StepSend
	ActionRecv  = StepReceive
	ActionFresh = StepFresh
	ActionBegin = StepBegin
	ActionEnd   = StepEnd

	RulePairing      = RulePair
	RulePairCompose  = RulePair
	RuleEnc          = RuleEncrypt
	RuleProjectLeft  = RuleFst
	RuleProjectRight = RuleSnd
	RuleDecryption   = RuleDecrypt
	RuleKnown        = RuleInitial
	RuleObserve      = RuleObserved
)

// Type aliases.
type (
	SearchResult      = Result
	Verdict           = Status
	Outcome           = Status
	AttackReport      = AttackTrace
	RoleScript        = Role
	SessionSpec       = Session
	Action            = Step
	Protocol          = Scenario
	AttackerKnowledge = Knowledge
	LimitsConfig      = Limits
	SearchOptions     = Options
	SearchBudget      = Budget
)

// Action constructor aliases.
func SendMessage(message any) Step       { return Send(message) }
func ReceiveMessage(pattern any) Step    { return Receive(pattern) }
func ReceivePattern(pattern any) Step    { return Receive(pattern) }
func GenerateFresh(names ...string) Step { return Fresh(names...) }
func BeginEvent(principal, nonce any) Step {
	return Begin(principal, nonce)
}
func EndEvent(principal, nonce any) Step {
	return End(principal, nonce)
}

// Scenario/session constructor aliases.
func NewProtocol(initial any, sessions ...Session) Scenario {
	return NewScenario(initial, sessions...)
}

// FromSessions constructs a scenario with no initial attacker knowledge.
func FromSessions(sessions ...Session) Scenario {
	return Scenario{Sessions: append([]Session(nil), sessions...)}
}
func NewSessionSpec(id string, role any, bindings any) Session {
	return NewSession(id, role, bindings)
}

// FindAttack is a descriptive alias for Analyze.
func FindAttack(scenario Scenario, settings ...any) (Result, error) {
	return Analyze(scenario, settings...)
}

// Explore is an alias for Analyze.
func Explore(scenario Scenario, settings ...any) (Result, error) {
	return Analyze(scenario, settings...)
}

// Verify replays a certificate against the supplied knowledge sources.
func (d *Derivation) Verify(initial, observed []Term, limits ...Limits) error {
	return VerifyDerivation(d, initial, observed, limits...)
}

// Derivable reports whether this knowledge snapshot can produce term, either
// by decomposition/replay or by a bounded new composition.
func (k *Knowledge) Derivable(term Term) bool {
	if !term.Valid() || !term.Ground() {
		return false
	}
	if k.Knows(term) {
		return true
	}
	candidates, err := k.Match(term)
	if err != nil {
		return false
	}
	for _, candidate := range candidates {
		if candidate.Term.Equal(term) {
			return true
		}
	}
	return false
}

// Terms returns the replayable decomposition terms. Use Match for newly
// composable messages.
func (k *Knowledge) Terms() []Term {
	out := make([]Term, 0, len(k.decompOrder))
	for _, derivation := range k.decompOrder {
		out = append(out, derivation.Term)
	}
	return out
}

// Certificates returns all attacker-message proofs in the attack trace.
func (r Result) Certificates() []*Certificate {
	if r.Attack == nil {
		return nil
	}
	return append([]*Certificate(nil), r.Attack.Certificates...)
}
