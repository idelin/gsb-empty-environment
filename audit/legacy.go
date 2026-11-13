// Package audit contains the deterministic attack search for the deliberately
// vulnerable legacy implementation under review.
//
// The legacy model mirrors the unsafe pattern the task describes:
//   - Capture retains the thread token's original level instead of downgrading.
//   - Request end and connection reuse do not revoke captured identities.
//   - A callback runs with whatever token is currently installed on the pooled
//     thread; the task handle is never revalidated.
//
// It is a state-machine fixture, not production code.
package audit

import (
	"fmt"
	"strings"

	"example.org/netsec/q0113"
)

type actionKind uint8

const (
	actionAuth actionKind = iota
	actionEnter
	actionNest
	actionExit
	actionSpawn
	actionEnd
	actionStart
	actionLocal
	actionRemote
)

type action struct {
	kind    actionKind
	request int
}

func (a action) String() string {
	names := [...]string{
		"auth",
		"enter",
		"nest",
		"exit",
		"spawn-capture",
		"end",
		"start-callback",
		"access-local",
		"access-remote",
	}
	return fmt.Sprintf("%s(R%d)", names[a.kind], a.request+1)
}

var allActions = []action{
	{actionAuth, 0}, {actionAuth, 1},
	{actionEnter, 0}, {actionEnter, 1},
	{actionNest, 0}, {actionNest, 1},
	{actionExit, 0}, {actionExit, 1},
	{actionSpawn, 0}, {actionSpawn, 1},
	{actionEnd, 0}, {actionEnd, 1},
	{actionStart, 0}, {actionStart, 1},
	{actionLocal, 0}, {actionRemote, 0},
	{actionLocal, 1}, {actionRemote, 1},
}

// Step is one scheduler event in a counterexample trace.
type Step struct {
	Action string
	Detail string
}

// Trace is a shortest interleaving that reaches a stale or cross-request
// access. Steps are in execution order.
type Trace struct {
	Steps     []Step
	Violation string
	Coverage  bool
}

func (t Trace) String() string {
	var b strings.Builder
	for i, step := range t.Steps {
		fmt.Fprintf(&b, "%02d  %-18s %s\n", i+1, step.Action, step.Detail)
	}
	fmt.Fprintf(&b, "violation: %s\n", t.Violation)
	return b.String()
}

type legacyToken struct {
	id         uint64
	owner      int
	connection string
	request    string
	generation uint64
	level      netsec.Level
	deadline   netsec.Instant
	nested     bool
}

func (t legacyToken) String() string {
	return fmt.Sprintf("%s/%d@%s(gen=%d,%s)", t.request, t.owner+1,
		t.connection, t.generation, t.level)
}

type legacyRequest struct {
	id       string
	gen      uint64
	level    netsec.Level
	deadline netsec.Instant
	active   bool
	ended    bool
	entered  bool
	nested   bool
	token    legacyToken
}

type legacyTask struct {
	owner   int
	token   legacyToken
	started bool
}

type coverageFlags struct {
	nested        bool
	outerRestored bool
	threadPool    bool
	remote        bool
}

func (f coverageFlags) complete() bool {
	return f.nested && f.outerRestored && f.threadPool && f.remote
}

type state struct {
	connGen uint64
	nextID  uint64

	requests [2]*legacyRequest
	stack    []legacyToken
	tasks    [2]*legacyTask
	flags    coverageFlags

	steps []Step
}

func newState() *state {
	return &state{}
}

func (s *state) clone() *state {
	out := &state{
		connGen: s.connGen,
		nextID:  s.nextID,
		flags:   s.flags,
		steps:   append([]Step(nil), s.steps...),
	}
	for i, req := range s.requests {
		if req == nil {
			continue
		}
		copyReq := *req
		out.requests[i] = &copyReq
	}
	if len(s.stack) > 0 {
		out.stack = append([]legacyToken(nil), s.stack...)
	}
	for i, task := range s.tasks {
		if task == nil {
			continue
		}
		copyTask := *task
		out.tasks[i] = &copyTask
	}
	return out
}

func (s *state) tokenID() uint64 {
	s.nextID++
	return s.nextID
}

func (s *state) activeRequest() *legacyRequest {
	for _, req := range s.requests {
		if req != nil && req.active {
			return req
		}
	}
	return nil
}

func (s *state) apply(a action) (applied bool, violation string) {
	switch a.kind {
	case actionAuth:
		return s.auth(a.request)
	case actionEnter:
		return s.enter(a.request)
	case actionNest:
		return s.nest(a.request)
	case actionExit:
		return s.exit(a.request)
	case actionSpawn:
		return s.spawn(a.request)
	case actionEnd:
		return s.end(a.request)
	case actionStart:
		return s.start(a.request)
	case actionLocal:
		return s.access(a.request, netsec.LocalResource)
	case actionRemote:
		return s.access(a.request, netsec.RemoteResource)
	default:
		return false, ""
	}
}

func (s *state) auth(i int) (bool, string) {
	if s.requests[i] != nil || s.activeRequest() != nil {
		return false, ""
	}
	s.connGen++
	req := &legacyRequest{
		id:       fmt.Sprintf("R%d", i+1),
		gen:      s.connGen,
		level:    netsec.Delegation,
		deadline: netsec.Instant(10_000),
		active:   true,
	}
	req.token = legacyToken{
		id:         s.tokenID(),
		owner:      i,
		connection: "C1",
		request:    req.id,
		generation: req.gen,
		level:      req.level,
		deadline:   req.deadline,
	}
	s.requests[i] = req
	s.steps = append(s.steps, Step{
		Action: action{actionAuth, i}.String(),
		Detail: fmt.Sprintf("authenticated %s on C1 generation %d (%s)",
			req.id, req.gen, req.level),
	})
	return true, ""
}

func (s *state) enter(i int) (bool, string) {
	req := s.requests[i]
	if req == nil || !req.active || req.entered {
		return false, ""
	}
	req.entered = true
	if len(s.stack) > 0 {
		s.flags.threadPool = true
	}
	s.stack = append(s.stack, req.token)
	s.steps = append(s.steps, Step{
		Action: action{actionEnter, i}.String(),
		Detail: fmt.Sprintf("pushed %s; depth=%d", req.token, len(s.stack)),
	})
	return true, ""
}

func (s *state) nest(i int) (bool, string) {
	req := s.requests[i]
	if req == nil || !req.active || !req.entered || req.nested {
		return false, ""
	}
	req.nested = true
	nested := legacyToken{
		id:         s.tokenID(),
		owner:      i,
		connection: req.token.connection,
		request:    req.id,
		generation: req.gen,
		level:      netsec.Identification,
		deadline:   req.deadline,
		nested:     true,
	}
	s.stack = append(s.stack, nested)
	s.flags.nested = true
	s.steps = append(s.steps, Step{
		Action: action{actionNest, i}.String(),
		Detail: fmt.Sprintf("pushed nested %s; depth=%d", nested, len(s.stack)),
	})
	return true, ""
}

func (s *state) exit(i int) (bool, string) {
	if len(s.stack) == 0 {
		return false, ""
	}
	top := s.stack[len(s.stack)-1]
	if top.owner != i {
		return false, ""
	}
	s.stack = s.stack[:len(s.stack)-1]
	if top.nested && len(s.stack) > 0 {
		next := s.stack[len(s.stack)-1]
		if next.owner == i && !next.nested {
			s.flags.outerRestored = true
		}
	}
	s.steps = append(s.steps, Step{
		Action: action{actionExit, i}.String(),
		Detail: fmt.Sprintf("popped %s; depth=%d", top, len(s.stack)),
	})
	return true, ""
}

func (s *state) spawn(i int) (bool, string) {
	req := s.requests[i]
	if req == nil || !req.active || s.tasks[i] != nil || len(s.stack) == 0 {
		return false, ""
	}
	captured := s.stack[len(s.stack)-1]
	s.tasks[i] = &legacyTask{
		owner: i,
		token: captured,
	}
	s.steps = append(s.steps, Step{
		Action: action{actionSpawn, i}.String(),
		Detail: fmt.Sprintf("Capture copied thread token %s without downgrade or generation check",
			captured),
	})
	return true, ""
}

func (s *state) end(i int) (bool, string) {
	req := s.requests[i]
	if req == nil || !req.active {
		return false, ""
	}
	req.active = false
	req.ended = true
	s.steps = append(s.steps, Step{
		Action: action{actionEnd, i}.String(),
		Detail: fmt.Sprintf("ended %s generation %d; stale thread frames and tasks were not revoked",
			req.id, req.gen),
	})
	return true, ""
}

func (s *state) start(i int) (bool, string) {
	task := s.tasks[i]
	if task == nil || task.started {
		return false, ""
	}
	task.started = true
	s.stack = append(s.stack, task.token)
	s.steps = append(s.steps, Step{
		Action: action{actionStart, i}.String(),
		Detail: fmt.Sprintf("callback R%d installed captured %s on the reused thread; handle not revalidated",
			i+1, task.token),
	})
	return true, ""
}

func (s *state) access(i int, target netsec.ResourceKind) (bool, string) {
	task := s.tasks[i]
	if task == nil || !task.started || len(s.stack) == 0 {
		return false, ""
	}
	top := s.stack[len(s.stack)-1]
	if top.deadline <= 0 {
		return false, ""
	}
	if target == netsec.LocalResource && top.level < netsec.Impersonation {
		return false, ""
	}
	if target == netsec.RemoteResource && top.level < netsec.Delegation {
		return false, ""
	}
	if target == netsec.RemoteResource {
		s.flags.remote = true
	}
	detail := fmt.Sprintf("callback R%d performed %s access as %s; thread token used without task-handle revalidation",
		i+1, target, top)
	kind := actionLocal
	if target == netsec.RemoteResource {
		kind = actionRemote
	}
	s.steps = append(s.steps, Step{
		Action: action{kind, i}.String(),
		Detail: detail,
	})

	ownerReq := s.requests[top.owner]
	switch {
	case ownerReq == nil:
		return true, fmt.Sprintf("callback R%d used an unrecognized identity", i+1)
	case !ownerReq.active || ownerReq.ended:
		return true, fmt.Sprintf(
			"callback R%d used ended identity %s generation %d (connection now generation %d)",
			i+1, ownerReq.id, top.generation, s.connGen)
	case top.generation != ownerReq.gen:
		return true, fmt.Sprintf(
			"callback R%d used stale identity %s generation %d while generation %d is current",
			i+1, ownerReq.id, top.generation, s.connGen)
	case top.owner != i:
		return true, fmt.Sprintf(
			"callback R%d used another request's identity %s generation %d",
			i+1, ownerReq.id, top.generation)
	default:
		return true, ""
	}
}

func (s *state) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "g%d|f%t%t%t%t|", s.connGen, s.flags.nested,
		s.flags.outerRestored, s.flags.threadPool, s.flags.remote)
	for _, req := range s.requests {
		if req == nil {
			b.WriteString("-|")
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d:%t:%t:%t:%t|", req.id, req.gen,
			req.level, req.active, req.ended, req.entered, req.nested)
	}
	b.WriteString("s")
	for _, tok := range s.stack {
		fmt.Fprintf(&b, "%d:%d:%d:%t,", tok.owner, tok.generation, tok.level, tok.nested)
	}
	b.WriteString("|t")
	for _, task := range s.tasks {
		if task == nil {
			b.WriteString("-,")
			continue
		}
		fmt.Fprintf(&b, "%d:%d:%d:%d:%t,", task.owner, task.token.owner,
			task.token.generation, task.token.level, task.started)
	}
	return b.String()
}

func (s *state) trace(violation string, coverage bool) Trace {
	return Trace{
		Steps:     append([]Step(nil), s.steps...),
		Violation: violation,
		Coverage:  coverage,
	}
}
