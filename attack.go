package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

const (
	// AttackInitiatorName is A in the attack model.
	AttackInitiatorName = "A"
	// AttackResponderName is B in the attack model.
	AttackResponderName = "B"
	// AttackMalloryName is the attacker's own registered identity. The attacker
	// may sign as Mallory, but may not forge A or B signatures.
	AttackMalloryName = "M"
	// AttackThirdName is an additional registered identity used by the model.
	AttackThirdName = "C"
	// MaxAttackSessions is the attacker/router concurrency limit.
	MaxAttackSessions = 3
)

var (
	// ErrAttackConcurrency is returned when a trace exceeds the session limit.
	ErrAttackConcurrency = errors.New("netsec: attack trace exceeds concurrent session limit")
	// ErrAttackForge is returned when a trace asks the attacker to forge a
	// signature under an identity it does not control.
	ErrAttackForge = errors.New("netsec: attacker cannot forge a signature")
	// ErrAttackModel is returned for malformed traces or model states.
	ErrAttackModel = errors.New("netsec: invalid attack trace")
	// ErrNoConfusion means the bounded search found no unknown-key-share witness.
	ErrNoConfusion = errors.New("netsec: no identity confusion found")
)

// AttackOp is one high-level router action in the attack trace.
type AttackOp uint8

const (
	// OpOpenInitiator starts an initiator endpoint (default A -> B).
	OpOpenInitiator AttackOp = iota + 1
	// OpRelayHello delivers a Hello to a responder and may rewrite its outer
	// initiator label.
	OpRelayHello
	// OpRelayResponse delivers a Response to the initiator and may rewrite its
	// outer responder label.
	OpRelayResponse
	// OpDeliverAuth delivers an initiator signature. The router may forward the
	// initiator's own signature or substitute a signature made by Mallory.
	OpDeliverAuth
	// OpDeliverConfirmA delivers A's confirmation to B.
	OpDeliverConfirmA
	// OpDeliverConfirmB delivers B's confirmation to A.
	OpDeliverConfirmB
)

func (op AttackOp) String() string {
	switch op {
	case OpOpenInitiator:
		return "open-initiator"
	case OpRelayHello:
		return "relay-hello"
	case OpRelayResponse:
		return "relay-response"
	case OpDeliverAuth:
		return "deliver-auth"
	case OpDeliverConfirmA:
		return "deliver-confirm-A"
	case OpDeliverConfirmB:
		return "deliver-confirm-B"
	default:
		return fmt.Sprintf("op(%d)", uint8(op))
	}
}

// AttackAction is one step. Empty fields select the model defaults.
type AttackAction struct {
	Op        AttackOp
	Session   int
	Initiator string // OpOpenInitiator, default A
	Peer      string // OpOpenInitiator, default B
	Responder string // OpRelayHello, default B
	Label     string // outer identity rewrite
	Signer    string // OpDeliverAuth, default initiator, or M
}

// Trace is an ordered attack script.
type Trace []AttackAction

// String renders the trace as one action per line.
func (t Trace) String() string {
	var builder strings.Builder
	for index, action := range t {
		fmt.Fprintf(&builder, "%d. %s session=%d", index+1, action.Op, action.Session)
		if action.Initiator != "" {
			fmt.Fprintf(&builder, " initiator=%s", action.Initiator)
		}
		if action.Peer != "" {
			fmt.Fprintf(&builder, " peer=%s", action.Peer)
		}
		if action.Responder != "" {
			fmt.Fprintf(&builder, " responder=%s", action.Responder)
		}
		if action.Label != "" {
			fmt.Fprintf(&builder, " label=%s", action.Label)
		}
		if action.Signer != "" {
			fmt.Fprintf(&builder, " signer=%s", action.Signer)
		}
		builder.WriteByte('\n')
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

// TraceResult describes the strongest result reached by a trace.
type TraceResult struct {
	Mode            Mode
	Confused        bool
	MatchingKeys    bool
	Depth           int
	Sessions        int
	Initiator       string
	Responder       string
	InitiatorPeer   string
	ResponderPeer   string
	Keys            KeySchedule
	InitiatorDigest [32]byte
	ResponderDigest [32]byte
}

// ConfusionWitness is a weak-mode unknown-key-share trace plus its proof data.
type ConfusionWitness struct {
	Trace  Trace
	Result TraceResult
}

type attackModel struct {
	registry *Registry
	parties  map[string]*Party
}

type attackSession struct {
	session   []byte
	initName  string
	init      *Initiator
	hello     *Hello
	responder *Responder
	response  *Response
	auth      *InitiatorAuth
	confirmA  bool
	confirmB  bool
}

type attackState struct {
	model    *attackModel
	mode     Mode
	sessions map[int]*attackSession
}

func newAttackModel() (*attackModel, error) {
	registry := NewRegistry()
	model := &attackModel{registry: registry, parties: make(map[string]*Party)}
	for _, name := range []string{AttackInitiatorName, AttackResponderName, AttackMalloryName, AttackThirdName} {
		seed := sha256.Sum256([]byte("example.org/netsec/q0075/attack-identity/" + name))
		private := ed25519.NewKeyFromSeed(seed[:])
		public := private.Public().(ed25519.PublicKey)
		if err := registry.Register(name, public); err != nil {
			return nil, err
		}
		party, err := NewParty(name, private, registry)
		if err != nil {
			return nil, err
		}
		model.parties[name] = party
	}
	return model, nil
}

func (m *attackModel) ephemeral(session int, role, party string) (*EphemeralKey, error) {
	seed := fmt.Sprintf("example.org/netsec/q0075/attack-ephemeral/%d/%s/%s", session, role, party)
	return EphemeralFromScalar(deterministicScalar([]byte(seed)))
}

func newAttackState(mode Mode) (*attackState, error) {
	model, err := newAttackModel()
	if err != nil {
		return nil, err
	}
	return &attackState{model: model, mode: mode, sessions: make(map[int]*attackSession)}, nil
}

func (s *attackState) session(number int) (*attackSession, error) {
	entry, ok := s.sessions[number]
	if !ok {
		return nil, fmt.Errorf("%w: session %d not open", ErrAttackModel, number)
	}
	return entry, nil
}

// RunTrace executes an attack script against a fresh deterministic model. It is
// the same executor for weak and hardened modes; only the mode parameter
// changes.
func RunTrace(mode Mode, trace Trace) (*TraceResult, error) {
	if !mode.IsValid() {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMode, mode)
	}
	counted := make(map[int]struct{})
	for _, action := range trace {
		if action.Session <= 0 {
			return nil, fmt.Errorf("%w: non-positive session", ErrAttackModel)
		}
		if action.Op == OpOpenInitiator {
			counted[action.Session] = struct{}{}
		}
	}
	if len(counted) > MaxAttackSessions {
		return nil, fmt.Errorf("%w: %d > %d", ErrAttackConcurrency, len(counted), MaxAttackSessions)
	}

	state, err := newAttackState(mode)
	if err != nil {
		return nil, err
	}
	for index, action := range trace {
		if err := state.apply(action); err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", index+1, action.Op, err)
		}
	}
	result, err := state.result()
	if err != nil {
		return nil, err
	}
	result.Depth = len(trace)
	result.Sessions = len(counted)
	return result, nil
}

func (s *attackState) apply(action AttackAction) error {
	switch action.Op {
	case OpOpenInitiator:
		if _, ok := s.sessions[action.Session]; ok {
			return fmt.Errorf("%w: session %d already open", ErrAttackModel, action.Session)
		}
		initName := action.Initiator
		if initName == "" {
			initName = AttackInitiatorName
		}
		peerName := action.Peer
		if peerName == "" {
			peerName = AttackResponderName
		}
		party := s.model.parties[initName]
		if party == nil {
			return fmt.Errorf("%w: unknown initiator %q", ErrAttackModel, initName)
		}
		if s.model.parties[peerName] == nil {
			return fmt.Errorf("%w: unknown peer %q", ErrAttackModel, peerName)
		}
		ephemeral, err := s.model.ephemeral(action.Session, "initiator", initName)
		if err != nil {
			return err
		}
		initiator, hello, err := NewInitiator(party, InitiatorConfig{
			Mode:      s.mode,
			SessionID: []byte(fmt.Sprintf("q0075/attack/session-%d", action.Session)),
			Peer:      peerName,
			Ephemeral: ephemeral,
		})
		if err != nil {
			return err
		}
		s.sessions[action.Session] = &attackSession{
			session:  append([]byte(nil), hello.SessionID...),
			initName: initName,
			init:     initiator,
			hello:    hello,
		}
		return nil

	case OpRelayHello:
		entry, err := s.session(action.Session)
		if err != nil {
			return err
		}
		if entry.hello == nil || entry.responder != nil {
			return fmt.Errorf("%w: hello already delivered", ErrAttackModel)
		}
		respName := action.Responder
		if respName == "" {
			respName = AttackResponderName
		}
		party := s.model.parties[respName]
		if party == nil {
			return fmt.Errorf("%w: unknown responder %q", ErrAttackModel, respName)
		}
		label := action.Label
		if label == "" {
			label = entry.hello.Initiator
		}
		if s.model.parties[label] == nil {
			return fmt.Errorf("%w: unknown initiator label %q", ErrAttackModel, label)
		}
		hello := cloneHello(entry.hello)
		hello.Initiator = label
		ephemeral, err := s.model.ephemeral(action.Session, "responder", respName)
		if err != nil {
			return err
		}
		responder, response, err := NewResponder(party, ResponderConfig{
			Mode:      s.mode,
			Ephemeral: ephemeral,
		}, hello)
		if err != nil {
			return err
		}
		entry.responder = responder
		entry.response = response
		return nil

	case OpRelayResponse:
		entry, err := s.session(action.Session)
		if err != nil {
			return err
		}
		if entry.response == nil || entry.auth != nil {
			return fmt.Errorf("%w: response not available or already delivered", ErrAttackModel)
		}
		label := action.Label
		if label == "" {
			label = entry.response.Responder
		}
		if s.model.parties[label] == nil {
			return fmt.Errorf("%w: unknown responder label %q", ErrAttackModel, label)
		}
		response := cloneResponse(entry.response)
		response.Responder = label
		auth, err := entry.init.HandleResponse(response)
		if err != nil {
			return err
		}
		entry.auth = cloneAuth(auth)
		return nil

	case OpDeliverAuth:
		entry, err := s.session(action.Session)
		if err != nil {
			return err
		}
		if entry.responder == nil || entry.auth == nil {
			return fmt.Errorf("%w: auth requires a verified response", ErrAttackModel)
		}
		signer := action.Signer
		if signer == "" {
			signer = entry.initName
		}
		var auth *InitiatorAuth
		switch {
		case signer == entry.initName:
			auth = cloneAuth(entry.auth)
		case signer == AttackMalloryName:
			digest := entry.responder.digest
			private := s.model.parties[AttackMalloryName].PrivateKey
			auth = &InitiatorAuth{
				SessionID: append([]byte(nil), entry.session...),
				Initiator: AttackMalloryName,
				Signature: ed25519.Sign(private, digest[:]),
			}
		default:
			return fmt.Errorf("%w: signer %q", ErrAttackForge, signer)
		}
		if err := entry.responder.HandleInitiatorAuth(auth); err != nil {
			return err
		}
		return nil

	case OpDeliverConfirmA:
		entry, err := s.session(action.Session)
		if err != nil {
			return err
		}
		if entry.init == nil || entry.responder == nil || entry.confirmA {
			return fmt.Errorf("%w: confirmation A not ready", ErrAttackModel)
		}
		confirmation, err := entry.init.SendConfirmA()
		if err != nil {
			return err
		}
		if err := entry.responder.HandleInitiatorConfirm(confirmation); err != nil {
			return err
		}
		entry.confirmA = true
		return nil

	case OpDeliverConfirmB:
		entry, err := s.session(action.Session)
		if err != nil {
			return err
		}
		if entry.responder == nil || entry.init == nil || entry.confirmB {
			return fmt.Errorf("%w: confirmation B not ready", ErrAttackModel)
		}
		confirmation, err := entry.responder.SendResponderConfirm()
		if err != nil {
			return err
		}
		if err := entry.init.VerifyResponderConfirm(confirmation); err != nil {
			return err
		}
		entry.confirmB = true
		return nil

	default:
		return fmt.Errorf("%w: unknown operation", ErrAttackModel)
	}
}

func (s *attackState) result() (*TraceResult, error) {
	result := &TraceResult{Mode: s.mode}
	for _, entry := range s.sessions {
		if entry.init == nil || entry.responder == nil || !entry.init.peerConfirmed || !entry.responder.confirmBSent {
			continue
		}
		aKeys, err := entry.init.Keys()
		if err != nil {
			continue
		}
		bKeys, err := entry.responder.Keys()
		if err != nil {
			continue
		}
		matching := aKeys.Equal(bKeys)
		peerAtA := entry.init.peer.Name
		peerAtB := entry.responder.peer.Name
		localA := entry.init.party.ID.Name
		localB := entry.responder.party.ID.Name
		confused := matching && (peerAtA != localB || peerAtB != localA)
		if !result.Confused || (confused && result.Depth == 0) {
			result.MatchingKeys = matching
			result.Initiator = localA
			result.Responder = localB
			result.InitiatorPeer = peerAtA
			result.ResponderPeer = peerAtB
			result.Keys = aKeys
			result.InitiatorDigest, _ = entry.init.HandshakeHash()
			result.ResponderDigest = entry.responder.HandshakeHash()
			result.Confused = confused
		}
	}
	return result, nil
}

func replay(mode Mode, trace Trace) (*attackState, error) {
	state, err := newAttackState(mode)
	if err != nil {
		return nil, err
	}
	for _, action := range trace {
		if err := state.apply(action); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func candidateActions(state *attackState) []AttackAction {
	if len(state.sessions) == 0 {
		return []AttackAction{{Op: OpOpenInitiator, Session: 1}}
	}
	var actions []AttackAction
	for number, entry := range state.sessions {
		switch {
		case entry.hello != nil && entry.responder == nil:
			for _, label := range []string{
				AttackInitiatorName, AttackResponderName,
				AttackThirdName, AttackMalloryName,
			} {
				actions = append(actions, AttackAction{
					Op: OpRelayHello, Session: number,
					Responder: AttackResponderName, Label: label,
				})
			}
		case entry.response != nil && entry.auth == nil:
			actions = append(actions, AttackAction{
				Op: OpRelayResponse, Session: number,
				Label: entry.response.Responder,
			})
		case entry.responder != nil && entry.auth != nil && !entry.responder.authVerified:
			for _, signer := range []string{entry.initName, AttackMalloryName} {
				actions = append(actions, AttackAction{
					Op: OpDeliverAuth, Session: number, Signer: signer,
				})
			}
		case entry.responder != nil && entry.responder.authVerified && !entry.confirmA:
			actions = append(actions, AttackAction{Op: OpDeliverConfirmA, Session: number})
		case entry.responder != nil && entry.confirmA && !entry.confirmB:
			actions = append(actions, AttackAction{Op: OpDeliverConfirmB, Session: number})
		}
	}
	return actions
}

func search(mode Mode, maxDepth int) (Trace, *TraceResult, error) {
	for depth := 1; depth <= maxDepth; depth++ {
		trace, result, err := searchDepth(mode, nil, depth)
		if err != nil {
			return nil, nil, err
		}
		if trace != nil {
			return trace, result, nil
		}
	}
	return nil, nil, nil
}

func searchDepth(mode Mode, prefix Trace, remaining int) (Trace, *TraceResult, error) {
	state, err := replay(mode, prefix)
	if err != nil {
		return nil, nil, err
	}
	if result, err := state.result(); err == nil && result.Confused {
		result.Depth = len(prefix)
		result.Sessions = len(state.sessions)
		return prefix, result, nil
	}
	if remaining == 0 {
		return nil, nil, nil
	}
	for _, action := range candidateActions(state) {
		next := append(append(Trace(nil), prefix...), action)
		child, err := replay(mode, next)
		if err != nil {
			continue
		}
		result, err := child.result()
		if err != nil {
			continue
		}
		if result.Confused {
			result.Depth = len(next)
			result.Sessions = len(child.sessions)
			return next, result, nil
		}
		if remaining > 1 {
			trace, found, err := searchDepth(mode, next, remaining-1)
			if err != nil {
				return nil, nil, err
			}
			if trace != nil {
				return trace, found, nil
			}
		}
	}
	return nil, nil, nil
}

// FindShortestIdentityConfusion searches the weak protocol for the shortest
// trace in which both endpoints hold the same key schedule but disagree about
// the peer identity. The search executor supports up to MaxAttackSessions
// concurrent sessions; the minimal witness itself needs one active session and
// an attacker-owned identity for a substituted signature.
func FindShortestIdentityConfusion() (*ConfusionWitness, error) {
	trace, result, err := search(ModeWeak, 10)
	if err != nil {
		return nil, err
	}
	if trace == nil {
		return nil, ErrNoConfusion
	}
	result.Depth = len(trace)
	return &ConfusionWitness{Trace: trace, Result: *result}, nil
}

// ProveHardenedRejects applies the exact weak witness trace to the hardened
// protocol and also exhaustively searches the same action alphabet to the
// witness depth. A nil return means the hardened protocol has no confusion in
// that action set.
func ProveHardenedRejects(witness *ConfusionWitness) error {
	if witness == nil {
		return errors.New("netsec: nil confusion witness")
	}
	replayed, err := RunTrace(ModeHardened, witness.Trace)
	if err == nil && replayed.Confused {
		return errors.New("netsec: hardened protocol accepted the weak witness")
	}
	if err == nil && replayed.MatchingKeys {
		return errors.New("netsec: hardened protocol reached matching keys on the weak witness")
	}
	if err != nil && (errors.Is(err, ErrAttackModel) || errors.Is(err, ErrAttackConcurrency)) {
		return err
	}
	found, _, err := search(ModeHardened, len(witness.Trace))
	if err != nil {
		return err
	}
	if found != nil {
		return errors.New("netsec: hardened protocol has a confusion trace in the same action set")
	}
	return nil
}
