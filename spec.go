package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DefaultWrongLimit is the fixed number of wrong answers tolerated before a
// challenge is permanently locked. A history may override it through
// History.WrongLimit.
const DefaultWrongLimit = 5

// Kind identifies one of the five authentication-service operations whose
// linearization intervals are recorded in a history.
type Kind int

const (
	// Create issues a fresh challenge and makes it the current challenge.
	Create Kind = iota + 1
	// Replace atomically retires the current challenge and installs a new one.
	Replace
	// Answer submits a code against one challenge without consuming it.
	Answer
	// Consume marks one challenge as spent without issuing an authorization.
	Consume
	// Grant authorizes exactly once, and only for the current challenge.
	Grant
)

// String returns the stable lowercase name of the operation kind.
func (k Kind) String() string {
	switch k {
	case Create:
		return "create"
	case Replace:
		return "replace"
	case Answer:
		return "answer"
	case Consume:
		return "consume"
	case Grant:
		return "grant"
	default:
		return "unknown"
	}
}

// Result is the outcome observed when an operation returned.
type Result int

const (
	// OK is the only successful result.
	OK Result = iota
	// ErrNoChallenge means the referenced challenge was never created.
	ErrNoChallenge
	// ErrExpired means a stale (non-current) challenge tried to authorize or
	// to replace the current challenge.
	ErrExpired
	// ErrWrongAnswer means the submitted code did not match but the failure
	// budget was not exhausted.
	ErrWrongAnswer
	// ErrConsumed means the challenge has already been spent.
	ErrConsumed
	// ErrLocked means the fixed wrong-answer budget was exhausted.
	ErrLocked
	// ErrReplay means a challenge identifier is being reused.
	ErrReplay
)

// String returns the stable lowercase name of the result.
func (r Result) String() string {
	switch r {
	case OK:
		return "ok"
	case ErrNoChallenge:
		return "no_challenge"
	case ErrExpired:
		return "expired"
	case ErrWrongAnswer:
		return "wrong_answer"
	case ErrConsumed:
		return "consumed"
	case ErrLocked:
		return "locked"
	case ErrReplay:
		return "replay"
	default:
		return "unknown"
	}
}

// Operation is one completed invocation with its real-time linearization
// interval [Invoke, Return] and the result observed at Return.
//
// Field meaning per kind:
//   - Create:   ChallengeID is the new challenge, Answer is its secret code.
//   - Replace:  ChallengeID is the old challenge, ReplacementID the new one,
//     Answer the new secret code.
//   - Answer:   ChallengeID is the target, Answer the submitted code.
//   - Consume:  ChallengeID is the target.
//   - Grant:    ChallengeID is the challenge requesting authorization.
type Operation struct {
	ID            int    `json:"id"`
	Kind          Kind   `json:"kind"`
	ChallengeID   string `json:"challenge_id"`
	ReplacementID string `json:"replacement_id,omitempty"`
	Answer        string `json:"answer,omitempty"`
	Invoke        int64  `json:"invoke"`
	Return        int64  `json:"return"`
	Result        Result `json:"result"`
}

// History is a finite set of completed operations. Order is irrelevant: the
// checker derives every legal ordering from the invoke/return intervals.
type History struct {
	Operations []Operation `json:"operations"`
	// WrongLimit overrides DefaultWrongLimit when positive.
	WrongLimit int `json:"wrong_limit,omitempty"`
}

// Validate reports structural problems that make a history uncheckable.
func (h History) Validate() error {
	ids := make(map[int]struct{}, len(h.Operations))
	for _, op := range h.Operations {
		if op.Kind < Create || op.Kind > Grant {
			return fmt.Errorf("operation %d: unknown kind %d", op.ID, op.Kind)
		}
		if op.Invoke > op.Return {
			return fmt.Errorf("operation %d: invoke %d after return %d", op.ID, op.Invoke, op.Return)
		}
		if op.ChallengeID == "" && op.Kind != Create && op.Kind != Replace {
			return fmt.Errorf("operation %d: empty challenge id", op.ID)
		}
		if op.Kind == Create && op.ChallengeID == "" {
			return fmt.Errorf("operation %d: create requires a challenge id", op.ID)
		}
		if op.Kind == Replace && (op.ChallengeID == "" || op.ReplacementID == "") {
			return fmt.Errorf("operation %d: replace requires old and new challenge ids", op.ID)
		}
		if _, dup := ids[op.ID]; dup {
			return fmt.Errorf("operation %d: duplicate operation id", op.ID)
		}
		ids[op.ID] = struct{}{}
	}
	return nil
}

type challengePhase int

const (
	phaseActive challengePhase = iota
	phaseConsumed
)

type challenge struct {
	id       string
	code     string
	phase    challengePhase
	failures int
	locked   bool
}

// Spec is the sequential reference implementation of the challenge service.
// A concurrent history is linearizable exactly when some total ordering of
// its operations drives Spec from its initial state to the observed results.
type Spec struct {
	wrongLimit int
	current    string
	live       map[string]*challenge
}

// NewSpec returns the initial specification state.
func NewSpec(wrongLimit int) *Spec {
	if wrongLimit <= 0 {
		wrongLimit = DefaultWrongLimit
	}
	return &Spec{
		wrongLimit: wrongLimit,
		live:       make(map[string]*challenge),
	}
}

func (s *Spec) clone() *Spec {
	cp := &Spec{
		wrongLimit: s.wrongLimit,
		current:    s.current,
		live:       make(map[string]*challenge, len(s.live)),
	}
	for id, ch := range s.live {
		duplicate := *ch
		cp.live[id] = &duplicate
	}
	return cp
}

// digest identifies the concrete machine state for memoization.
func (s *Spec) digest() string {
	var b strings.Builder
	b.WriteString("cur=")
	b.WriteString(s.current)
	b.WriteString(";")
	ids := make([]string, 0, len(s.live))
	for id := range s.live {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ch := s.live[id]
		b.WriteString(id)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(int(ch.phase)))
		b.WriteString(":")
		b.WriteString(strconv.Itoa(ch.failures))
		b.WriteString(":")
		b.WriteString(strconv.FormatBool(ch.locked))
		b.WriteString(";")
	}
	return b.String()
}

// Apply executes one operation. It mutates the state only when the observed
// result matches what the reference implementation would produce, and reports
// whether it matched.
func (s *Spec) Apply(op Operation) bool {
	want := s.expected(op)
	if want != op.Result {
		return false
	}
	s.commit(op)
	return true
}

func (s *Spec) expected(op Operation) Result {
	switch op.Kind {
	case Create:
		if _, exists := s.live[op.ChallengeID]; exists {
			return ErrReplay
		}
		return OK
	case Replace:
		old, exists := s.live[op.ChallengeID]
		if !exists {
			return ErrNoChallenge
		}
		if s.current != old.id {
			return ErrExpired
		}
		if _, reused := s.live[op.ReplacementID]; reused {
			return ErrReplay
		}
		return OK
	case Answer:
		ch, exists := s.live[op.ChallengeID]
		if !exists {
			return ErrNoChallenge
		}
		if ch.locked {
			return ErrLocked
		}
		if ch.phase == phaseConsumed {
			return ErrConsumed
		}
		if ch.code == op.Answer {
			return OK
		}
		if ch.failures+1 >= s.wrongLimit {
			return ErrLocked
		}
		return ErrWrongAnswer
	case Consume:
		ch, exists := s.live[op.ChallengeID]
		if !exists {
			return ErrNoChallenge
		}
		if ch.locked {
			return ErrLocked
		}
		if ch.phase == phaseConsumed {
			return ErrConsumed
		}
		return OK
	case Grant:
		ch, exists := s.live[op.ChallengeID]
		if !exists {
			return ErrNoChallenge
		}
		if ch.locked {
			return ErrLocked
		}
		if ch.phase == phaseConsumed {
			return ErrConsumed
		}
		if s.current != ch.id {
			return ErrExpired
		}
		return OK
	default:
		return ErrNoChallenge
	}
}

func (s *Spec) commit(op Operation) {
	switch op.Kind {
	case Create:
		s.live[op.ChallengeID] = &challenge{id: op.ChallengeID, code: op.Answer}
		if s.current == "" {
			s.current = op.ChallengeID
		}
	case Replace:
		s.live[op.ReplacementID] = &challenge{id: op.ReplacementID, code: op.Answer}
		s.current = op.ReplacementID
	case Answer:
		ch, exists := s.live[op.ChallengeID]
		if !exists {
			return
		}
		if ch.locked || ch.phase == phaseConsumed || ch.code == op.Answer {
			return
		}
		ch.failures++
		if ch.failures >= s.wrongLimit {
			ch.locked = true
		}
	case Consume:
		if ch, exists := s.live[op.ChallengeID]; exists {
			ch.phase = phaseConsumed
		}
	case Grant:
		if ch, exists := s.live[op.ChallengeID]; exists {
			ch.phase = phaseConsumed
		}
	}
}
