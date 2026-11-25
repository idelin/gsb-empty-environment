package symexec

import (
	"errors"
	"fmt"
)

// VerifyTrace replays an attack trace against the scenario. It checks role
// ordering, receive matching, freshness, event ordering, and every attacker
// message's derivation certificate.
func VerifyTrace(scenario Scenario, trace []TraceStep, settings ...any) error {
	if err := scenario.Validate(); err != nil {
		return err
	}
	options := optionsFrom(settings)
	options = options.normalized(scenario)
	initial := scenario.initialTerms()

	type replaySession struct {
		id       string
		pos      int
		bindings map[string]Term
	}
	sessions := make(map[string]*replaySession, len(scenario.Sessions))
	for _, session := range scenario.Sessions {
		sessions[session.ID] = &replaySession{
			id:       session.ID,
			bindings: cloneBindings(session.Bindings),
		}
	}

	var observed []Term
	var events []Event
	begins := make(map[string]Event)

	for i, step := range trace {
		if step.Index != i+1 {
			return fmt.Errorf("trace step %d has index %d", i, step.Index)
		}
		session, ok := sessions[step.Session]
		if !ok {
			return fmt.Errorf("trace step %d references unknown session %q", i, step.Session)
		}
		definition := scenarioSession(scenario, step.Session)
		if definition == nil {
			return fmt.Errorf("trace step %d has no session definition", i)
		}
		if step.Role != definition.Role.Name {
			return fmt.Errorf("trace step %d role %q does not match session role %q",
				i, step.Role, definition.Role.Name)
		}
		if session.pos >= len(definition.Role.Steps) {
			return fmt.Errorf("session %q executes past the end of its role", step.Session)
		}
		roleStep := definition.Role.Steps[session.pos]
		if step.StepIndex != session.pos {
			return fmt.Errorf("trace step %d for session %q has role index %d, want %d",
				i, step.Session, step.StepIndex, session.pos)
		}
		if step.Kind != roleStep.Kind {
			return fmt.Errorf("trace step %d kind %s does not match role step kind %s",
				i, step.Kind, roleStep.Kind)
		}

		switch step.Kind {
		case StepSend:
			if step.Message == nil {
				return fmt.Errorf("trace step %d send has no message", i)
			}
			expected, err := roleStep.Term.Substitute(session.bindings)
			if err != nil {
				return fmt.Errorf("trace step %d send cannot be instantiated: %w", i, err)
			}
			if !expected.Equal(*step.Message) {
				return fmt.Errorf("trace step %d sends %s, expected %s", i, *step.Message, expected)
			}
			observed = append(observed, *step.Message)

		case StepReceive:
			if step.Message == nil || step.Certificate == nil {
				return fmt.Errorf("trace step %d receive has no attacker message or certificate", i)
			}
			if !step.Certificate.Term.Equal(*step.Message) {
				return fmt.Errorf("trace step %d certificate is for %s, message is %s",
					i, step.Certificate.Term, *step.Message)
			}
			knowledge, err := NewKnowledge(initial, observed, options.Limits)
			if err != nil {
				return fmt.Errorf("trace step %d cannot build attacker knowledge: %w", i, err)
			}
			if err := knowledge.Verify(step.Certificate); err != nil {
				return fmt.Errorf("trace step %d attacker message %s is not constructible: %w",
					i, *step.Message, err)
			}
			binding, ok := Match(*roleStep.Term, *step.Message, session.bindings)
			if !ok {
				return fmt.Errorf("trace step %d message %s does not match receive pattern %s",
					i, *step.Message, *roleStep.Term)
			}
			session.bindings = binding

		case StepFresh:
			if len(step.Atoms) != len(roleStep.Fresh) {
				return fmt.Errorf("trace step %d generated %d atoms, want %d",
					i, len(step.Atoms), len(roleStep.Fresh))
			}
			binding := cloneBindings(session.bindings)
			atoms := make([]Term, 0, len(roleStep.Fresh))
			for _, name := range roleStep.Fresh {
				value := generateFreshAtom(step.Session, session.pos, name, initial, observed)
				binding[name] = value
				atoms = append(atoms, value)
			}
			if !termsEqual(atoms, step.Atoms) {
				return fmt.Errorf("trace step %d fresh atoms do not replay", i)
			}
			session.bindings = binding

		case StepBegin, StepEnd:
			if step.Event == nil {
				return fmt.Errorf("trace step %d %s has no event", i, step.Kind)
			}
			principal, err := roleStep.Principal.Substitute(session.bindings)
			if err != nil {
				return fmt.Errorf("trace step %d event principal cannot be instantiated: %w", i, err)
			}
			nonce, err := roleStep.Nonce.Substitute(session.bindings)
			if err != nil {
				return fmt.Errorf("trace step %d event nonce cannot be instantiated: %w", i, err)
			}
			event := Event{
				Kind:      step.Kind,
				Session:   step.Session,
				Role:      definition.Role.Name,
				StepIndex: session.pos,
				Index:     i + 1,
				Principal: principal,
				Nonce:     nonce,
			}
			if !eventsEqual(event, *step.Event) {
				return fmt.Errorf("trace step %d event does not replay", i)
			}
			events = append(events, event)
			if step.Kind == StepBegin {
				key := eventKeyOf(event)
				if _, ok := begins[key]; !ok {
					begins[key] = event
				}
			}

		default:
			return fmt.Errorf("trace step %d has unknown kind %d", i, step.Kind)
		}

		if step.Bindings != nil && !bindingsEqual(session.bindings, step.Bindings) {
			return fmt.Errorf("trace step %d bindings do not replay", i)
		}
		session.pos++
	}

	missing := firstMissingBegin(events)
	if missing == nil {
		return errors.New("trace contains no authentication mismatch")
	}
	if missing.Index != len(trace) || trace[len(trace)-1].Kind != StepEnd {
		return fmt.Errorf("first mismatch is at event %d, not at the end of the trace", missing.Index)
	}
	return nil
}

// VerifyAttack verifies a result produced by Analyze/Search.
func VerifyAttack(scenario Scenario, attack *AttackTrace, settings ...any) error {
	if attack == nil {
		return errors.New("nil attack trace")
	}
	if len(attack.Certificates) > 0 {
		var traceCerts []*Certificate
		for _, step := range attack.Trace {
			if step.Kind == StepReceive {
				traceCerts = append(traceCerts, step.Certificate)
			}
		}
		if len(traceCerts) != len(attack.Certificates) {
			return fmt.Errorf("attack has %d certificates, trace has %d receive messages",
				len(attack.Certificates), len(traceCerts))
		}
		for i := range traceCerts {
			if traceCerts[i] == nil || attack.Certificates[i] == nil ||
				!traceCerts[i].Term.Equal(attack.Certificates[i].Term) {
				return fmt.Errorf("attack certificate %d does not match the trace", i)
			}
		}
	}
	options := optionsFrom(settings)
	if options.Limits.MaxComposeDepth == 0 && options.MaxComposeDepth == 0 {
		options.Limits.MaxComposeDepth = attack.Limits.MaxComposeDepth
	}
	if options.Limits.MaxTerms == 0 {
		options.Limits.MaxTerms = attack.Limits.MaxTerms
	}
	return VerifyTrace(scenario, attack.Trace, options)
}

// VerifyCertificates checks only the attacker derivations, without replaying
// role ordering. It is useful to hosts that already validated their own trace
// shape.
func VerifyCertificates(scenario Scenario, trace []TraceStep, settings ...any) error {
	if err := scenario.Validate(); err != nil {
		return err
	}
	options := optionsFrom(settings)
	options = options.normalized(scenario)
	initial := scenario.initialTerms()
	var observed []Term
	for i, step := range trace {
		if step.Kind == StepSend && step.Message != nil {
			observed = append(observed, *step.Message)
			continue
		}
		if step.Kind != StepReceive {
			continue
		}
		if step.Certificate == nil || step.Message == nil {
			return fmt.Errorf("trace step %d receive lacks a certificate", i)
		}
		if !step.Certificate.Term.Equal(*step.Message) {
			return fmt.Errorf("trace step %d certificate/message mismatch", i)
		}
		knowledge, err := NewKnowledge(initial, observed, options.Limits)
		if err != nil {
			return err
		}
		if err := knowledge.Verify(step.Certificate); err != nil {
			return fmt.Errorf("trace step %d attacker message %s is not constructible: %w",
				i, *step.Message, err)
		}
	}
	return nil
}

// Verify replays an attack result.
func (r Result) Verify(scenario Scenario, settings ...any) error {
	if !r.IsAttack() {
		return nil
	}
	if r.Attack == nil {
		return fmt.Errorf("result status is %s but has no attack trace", r.Status)
	}
	options := optionsFrom(settings)
	if options.Limits.MaxComposeDepth == 0 && options.MaxComposeDepth == 0 {
		options.Limits.MaxComposeDepth = r.Limits.MaxComposeDepth
	}
	if options.Limits.MaxTerms == 0 {
		options.Limits.MaxTerms = r.Limits.MaxTerms
	}
	return VerifyAttack(scenario, r.Attack, options)
}

func scenarioSession(scenario Scenario, id string) *Session {
	for i := range scenario.Sessions {
		if scenario.Sessions[i].ID == id {
			return &scenario.Sessions[i]
		}
	}
	return nil
}

func termsEqual(a, b []Term) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func eventsEqual(a, b Event) bool {
	return a.Kind == b.Kind &&
		a.Session == b.Session &&
		a.Role == b.Role &&
		a.StepIndex == b.StepIndex &&
		a.Index == b.Index &&
		a.Principal.Equal(b.Principal) &&
		a.Nonce.Equal(b.Nonce)
}

func bindingsEqual(a, b map[string]Term) bool {
	if len(a) != len(b) {
		return false
	}
	for name, term := range a {
		other, ok := b[name]
		if !ok || !term.Equal(other) {
			return false
		}
	}
	return true
}

func firstMissingBegin(events []Event) *Event {
	begins := make(map[string]Event)
	for i := range events {
		event := &events[i]
		if event.Kind == StepBegin {
			key := eventKeyOf(*event)
			if _, ok := begins[key]; !ok {
				begins[key] = *event
			}
			continue
		}
		if _, ok := begins[eventKeyOf(*event)]; !ok {
			return event
		}
	}
	return nil
}
