package statelearning

import (
	"errors"
	"strings"
	"testing"
)

// Hidden states of the mock login service. The learner never sees these
// values directly; it can only observe Output values through the SUL.
const (
	mockAnonymous = iota
	mockAuthenticated
	mockDebug
	mockLocked
)

// Alphabet symbol names shared by the tests.
const (
	inLoginOK    = "login_ok"
	inLoginBad   = "login_bad"
	inLogout     = "logout"
	inAccess     = "access_protected"
	inDebugEnter = "debug_enter"
	inDebugExit  = "debug_exit"
)

// Response codes produced by the mock service.
const (
	respOK         = "ok"
	respForbidden  = "forbidden"
	respBadRequest = "bad_request"
	respLocked     = "locked"
)

var errMockReset = errors.New("mock login service: reset failed")
var errMockStep = errors.New("mock login service: transport error")

// mockLoginService is a deterministic SUL with hidden state.
//
// The authorization flaw under test is reachable from the anonymous
// initial state without a successful login: debug_enter followed by
// access_protected yields an authorized response, a two-input bypass path.
type mockLoginService struct {
	state      int
	failReset  bool
	failStepOn string
	resetCalls int
	stepCalls  int
}

func (m *mockLoginService) Reset() error {
	if m.failReset {
		return errMockReset
	}
	m.resetCalls++
	m.state = mockAnonymous
	return nil
}

func (m *mockLoginService) Step(in Input) (Output, error) {
	if m.failStepOn != "" && in.Name == m.failStepOn {
		return Output{}, errMockStep
	}
	m.stepCalls++

	switch m.state {
	case mockAnonymous:
		return m.stepAnonymous(in)
	case mockAuthenticated:
		return m.stepAuthenticated(in)
	case mockDebug:
		return m.stepDebug(in)
	default:
		return Output{Response: respLocked, Authorized: false}, nil
	}
}

func (m *mockLoginService) stepAnonymous(in Input) (Output, error) {
	switch in.Name {
	case inLoginOK:
		m.state = mockAuthenticated
		return Output{Response: respOK, Authorized: true}, nil
	case inLoginBad:
		// A single failed login immediately locks the service, giving the
		// anonymous state a distinct one-step successor that is observable
		// through a length-one suffix.
		m.state = mockLocked
		return Output{Response: respLocked, Authorized: false}, nil
	// Authorization bypass: a debug entry from the anonymous state is
	// accepted, after which protected access is authorized.
	case inDebugEnter:
		m.state = mockDebug
		return Output{Response: respOK, Authorized: true}, nil
	case inAccess:
		return Output{Response: respForbidden, Authorized: false}, nil
	case inLogout, inDebugExit:
		return Output{Response: respBadRequest, Authorized: false}, nil
	default:
		return Output{Response: respBadRequest, Authorized: false}, nil
	}
}

func (m *mockLoginService) stepAuthenticated(in Input) (Output, error) {
	switch in.Name {
	case inAccess:
		return Output{Response: respOK, Authorized: true}, nil
	case inLogout:
		m.state = mockAnonymous
		return Output{Response: respOK, Authorized: true}, nil
	case inLoginOK:
		return Output{Response: respOK, Authorized: true}, nil
	case inLoginBad:
		m.state = mockLocked
		return Output{Response: respLocked, Authorized: false}, nil
	case inDebugEnter, inDebugExit:
		return Output{Response: respBadRequest, Authorized: false}, nil
	default:
		return Output{Response: respBadRequest, Authorized: false}, nil
	}
}

func (m *mockLoginService) stepDebug(in Input) (Output, error) {
	switch in.Name {
	case inAccess:
		return Output{Response: respOK, Authorized: true}, nil
	case inDebugExit:
		m.state = mockAnonymous
		return Output{Response: respOK, Authorized: true}, nil
	case inLogout:
		m.state = mockAnonymous
		return Output{Response: respOK, Authorized: true}, nil
	default:
		return Output{Response: respBadRequest, Authorized: false}, nil
	}
}

func testAlphabet() []Input {
	return []Input{
		{Name: inLoginOK},
		{Name: inLoginBad},
		{Name: inLogout},
		{Name: inAccess},
		{Name: inDebugEnter},
		{Name: inDebugExit},
	}
}

func inputByName(name string) Input { return Input{Name: name} }

// TestLearnDiscoversAuthBypass verifies that the learner reconstructs the
// hidden service and exposes the short unauthenticated bypass path.
func TestLearnDiscoversAuthBypass(t *testing.T) {
	sul := &mockLoginService{}
	result := Learn(sul, Config{
		Alphabet:   testAlphabet(),
		MaxDepth:   4,
		MaxQueries: 0,
	})
	if result.Err != nil {
		t.Fatalf("unexpected learning error: %v", result.Err)
	}
	if result.Status != StatusComplete {
		t.Fatalf("expected complete model within depth, got %s (err=%v)", result.Status, result.Err)
	}
	if result.DepthReached != 4 {
		t.Fatalf("expected depth reached 4, got %d", result.DepthReached)
	}
	if result.Model == nil {
		t.Fatal("expected non-nil model")
	}

	// Four hidden states: anonymous, authenticated, debug, locked.
	if got := result.Model.StateCount(); got != 4 {
		t.Fatalf("expected 4 inferred states, got %d", got)
	}

	// Every membership query must have started with a reset.
	if sul.resetCalls != result.Queries {
		t.Fatalf("expected one reset per query: resets=%d queries=%d", sul.resetCalls, result.Queries)
	}

	// The two-input bypass: no login, but debug entry then protected access.
	bypass := []Input{inputByName(inDebugEnter), inputByName(inAccess)}
	trace, err := result.Model.Trace(bypass)
	if err != nil {
		t.Fatalf("model trace failed: %v", err)
	}
	if !trace[1].Authorized || trace[1].Response != respOK {
		t.Fatalf("bypass path not authorized: %+v", trace)
	}

	// Plain protected access without any credential remains forbidden.
	direct, err := result.Model.Trace([]Input{inputByName(inAccess)})
	if err != nil {
		t.Fatalf("model trace failed: %v", err)
	}
	if direct[0].Authorized || direct[0].Response != respForbidden {
		t.Fatalf("anonymous protected access should be forbidden: %+v", direct)
	}

	// Independent exhaustive cross-check of the learned model against the
	// SUL within the claimed depth.
	if err := crossCheck(t, sul, result.Model, testAlphabet(), 4); err != nil {
		t.Fatalf("learned model diverges from SUL: %v", err)
	}

	// Lockout path is also captured.
	lockout, err := result.Model.Trace([]Input{
		inputByName(inLoginBad),
		inputByName(inAccess),
	})
	if err != nil {
		t.Fatalf("model trace failed: %v", err)
	}
	if lockout[1].Response != respLocked || lockout[1].Authorized {
		t.Fatalf("expected locked unauthorized output, got %+v", lockout[1])
	}
}

// crossCheck enumerates every word up to depth on a fresh SUL and compares
// the traces with the hypothesis.
func crossCheck(t *testing.T, sul *mockLoginService, model *MealyMachine, alphabet []Input, depth int) error {
	t.Helper()
	frontier := [][]Input{{}}
	for d := 0; d < depth; d++ {
		var next [][]Input
		for _, word := range frontier {
			for _, in := range alphabet {
				extended := append(append([]Input(nil), word...), in)
				sulTrace := replayFresh(sul, extended)
				modelTrace, err := model.Trace(extended)
				if err != nil {
					return err
				}
				if len(sulTrace) != len(modelTrace) {
					return errors.New("trace length mismatch")
				}
				for i := range sulTrace {
					if sulTrace[i] != modelTrace[i] {
						return errors.New("trace mismatch for word " + wordString(extended))
					}
				}
				next = append(next, extended)
			}
		}
		frontier = next
	}
	return nil
}

func replayFresh(sul *mockLoginService, word []Input) []Output {
	if err := sul.Reset(); err != nil {
		panic(err)
	}
	out := make([]Output, 0, len(word))
	for _, in := range word {
		o, err := sul.Step(in)
		if err != nil {
			panic(err)
		}
		out = append(out, o)
	}
	return out
}

func wordString(word []Input) string {
	names := make([]string, len(word))
	for i, in := range word {
		names[i] = in.Name
	}
	return strings.Join(names, ",")
}

// TestLearnResetFailure ensures a SUL reset failure aborts learning with the
// error surfaced instead of being interpreted as an observation.
func TestLearnResetFailure(t *testing.T) {
	sul := &mockLoginService{failReset: true}
	result := Learn(sul, Config{
		Alphabet:   testAlphabet(),
		MaxDepth:   3,
		MaxQueries: 100,
	})
	if result.Status != StatusIncomplete {
		t.Fatalf("expected incomplete status, got %s", result.Status)
	}
	if !errors.Is(result.Err, errMockReset) {
		t.Fatalf("expected wrapped reset error, got %v", result.Err)
	}
	if !strings.Contains(result.Err.Error(), "reset failed") {
		t.Fatalf("expected reset failure context, got %v", result.Err)
	}
	if result.Queries != 0 {
		t.Fatalf("no successful query should be counted, got %d", result.Queries)
	}
}

// TestLearnStepFailure ensures a SUL step failure is reported rather than
// crashing the learner.
func TestLearnStepFailure(t *testing.T) {
	sul := &mockLoginService{failStepOn: inAccess}
	result := Learn(sul, Config{
		Alphabet:   testAlphabet(),
		MaxDepth:   3,
		MaxQueries: 100,
	})
	if result.Status != StatusIncomplete {
		t.Fatalf("expected incomplete status, got %s", result.Status)
	}
	if !errors.Is(result.Err, errMockStep) {
		t.Fatalf("expected wrapped step error, got %v", result.Err)
	}
}

// TestLearnBudgetIncomplete verifies that exceeding the query budget returns
// an incomplete result and never a falsely complete claim.
func TestLearnBudgetIncomplete(t *testing.T) {
	sul := &mockLoginService{}
	result := Learn(sul, Config{
		Alphabet:   testAlphabet(),
		MaxDepth:   5,
		MaxQueries: 3,
	})
	if result.Status != StatusIncomplete {
		t.Fatalf("expected incomplete status, got %s", result.Status)
	}
	if !errors.Is(result.Err, ErrBudgetExhausted) {
		t.Fatalf("expected ErrBudgetExhausted, got %v", result.Err)
	}
	if result.Queries > 3 {
		t.Fatalf("budget of 3 queries exceeded: %d", result.Queries)
	}
}

// TestLearnDepthLimitIsClaimBoundary checks that completion is scoped to the
// configured depth: depth zero only validates the empty word.
func TestLearnDepthLimitIsClaimBoundary(t *testing.T) {
	sul := &mockLoginService{}
	result := Learn(sul, Config{
		Alphabet:   testAlphabet(),
		MaxDepth:   0,
		MaxQueries: 0,
	})
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Status != StatusComplete {
		t.Fatalf("depth-0 conformance trivially holds, got %s", result.Status)
	}
	if result.DepthReached != 0 {
		t.Fatalf("expected depth reached 0, got %d", result.DepthReached)
	}
	if result.Model == nil || result.Model.StateCount() < 1 {
		t.Fatal("expected a usable hypothesis even at depth zero")
	}
}

// TestLearnInvalidConfig covers caller-facing configuration validation.
func TestLearnInvalidConfig(t *testing.T) {
	sul := &mockLoginService{}
	if res := Learn(nil, Config{Alphabet: testAlphabet(), MaxDepth: 1}); res.Err == nil {
		t.Fatal("expected error for nil SUL")
	}
	if res := Learn(sul, Config{MaxDepth: 1}); res.Err == nil {
		t.Fatal("expected error for empty alphabet")
	}
	if res := Learn(sul, Config{Alphabet: testAlphabet(), MaxDepth: -1}); res.Err == nil {
		t.Fatal("expected error for negative depth")
	}
	dup := append(testAlphabet(), Input{Name: inAccess})
	if res := Learn(sul, Config{Alphabet: dup, MaxDepth: 1}); res.Err == nil {
		t.Fatal("expected error for duplicate alphabet symbol")
	}
}

// TestLearnCounterexampleRefinement shows that a conformance counterexample
// is fed back into the observation table: a depth-1 run cannot distinguish all
// hidden states, while a deeper run splits them and converges to the same
// four-state model validated against the SUL.
func TestLearnCounterexampleRefinement(t *testing.T) {
	shallow := Learn(&mockLoginService{}, Config{
		Alphabet: testAlphabet(),
		MaxDepth: 1,
	})
	if shallow.Err != nil || shallow.Status != StatusComplete {
		t.Fatalf("shallow run should complete within depth 1: %+v", shallow)
	}

	deep := Learn(&mockLoginService{}, Config{
		Alphabet:   testAlphabet(),
		MaxDepth:   4,
		MaxQueries: 0,
	})
	if deep.Err != nil || deep.Status != StatusComplete {
		t.Fatalf("deep run failed: %+v", deep)
	}

	// Deeper conformance must split observationally equal rows at least as
	// finely as the depth-1 hypothesis.
	if deep.Model.StateCount() < shallow.Model.StateCount() {
		t.Fatalf("deeper learning produced fewer states: %d vs %d",
			deep.Model.StateCount(), shallow.Model.StateCount())
	}
	if deep.Model.StateCount() != 4 {
		t.Fatalf("expected 4 states after counterexample refinement, got %d",
			deep.Model.StateCount())
	}

	// The refined model must agree with the SUL on every word up to depth 4.
	if err := crossCheck(t, &mockLoginService{}, deep.Model, testAlphabet(), 4); err != nil {
		t.Fatalf("refined model diverges from SUL: %v", err)
	}
}
