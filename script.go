package batchclient

import (
	"context"
	"sync"
)

// StepFunc scripts the behavior of one SubmitBatch call. It receives the
// transport so steps can allocate monotonic commit versions via Commit.
type StepFunc func(t *ScriptTransport, batch []Request) ([]Response, error)

// ScriptTransport is an orchestratable Transport for tests and fault
// replay. Each call consumes one scripted step; once the script is
// exhausted it commits every requested item with a fresh version.
type ScriptTransport struct {
	mu       sync.Mutex
	steps    []StepFunc
	calls    [][]Request
	versions map[string]uint64
}

// NewScriptTransport creates a transport that replays the given steps in
// order, then falls back to committing every requested item.
func NewScriptTransport(steps ...StepFunc) *ScriptTransport {
	return &ScriptTransport{
		steps:    steps,
		versions: make(map[string]uint64),
	}
}

// SubmitBatch records the call and applies the next scripted step.
func (s *ScriptTransport) SubmitBatch(ctx context.Context, batch []Request) ([]Response, error) {
	s.mu.Lock()
	s.calls = append(s.calls, append([]Request(nil), batch...))
	var step StepFunc
	if len(s.steps) > 0 {
		step = s.steps[0]
		s.steps = s.steps[1:]
	}
	s.mu.Unlock()
	if step != nil {
		return step(s, batch)
	}
	return s.CommitAll(batch), nil
}

// Calls returns the recorded batches, in call order.
func (s *ScriptTransport) Calls() [][]Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]Request, len(s.calls))
	for i, c := range s.calls {
		out[i] = append([]Request(nil), c...)
	}
	return out
}

// Commit allocates the next monotonic commit version for id and returns a
// success response carrying it.
func (s *ScriptTransport) Commit(id, value string) Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[id]++
	return Response{ID: id, Version: s.versions[id], Value: value}
}

// CommitFail allocates the next monotonic commit version for id and returns
// a committed item-level failure carrying it.
func (s *ScriptTransport) CommitFail(id, errMsg string) Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[id]++
	return Response{ID: id, Version: s.versions[id], Err: errMsg}
}

// CommitAll commits every requested item, echoing its Data as the value.
func (s *ScriptTransport) CommitAll(batch []Request) []Response {
	resps := make([]Response, 0, len(batch))
	for _, req := range batch {
		resps = append(resps, s.Commit(req.ID, req.Data))
	}
	return resps
}

// Respond returns a step that replies with fixed responses.
func Respond(resps ...Response) StepFunc {
	return func(t *ScriptTransport, batch []Request) ([]Response, error) {
		return resps, nil
	}
}

// RespondErr returns a step that delivers partial responses together with a
// batch-level error. Confirmed items still count; only missing items retry.
func RespondErr(err error, resps ...Response) StepFunc {
	return func(t *ScriptTransport, batch []Request) ([]Response, error) {
		return resps, err
	}
}

// FailBatch returns a step that fails the whole batch with err.
func FailBatch(err error) StepFunc {
	return func(t *ScriptTransport, batch []Request) ([]Response, error) {
		return nil, err
	}
}

// CommitExcept returns a step that commits every requested item except the
// dropped IDs, simulating a partially missing response.
func CommitExcept(drops ...string) StepFunc {
	dropped := make(map[string]bool, len(drops))
	for _, id := range drops {
		dropped[id] = true
	}
	return func(t *ScriptTransport, batch []Request) ([]Response, error) {
		var resps []Response
		for _, req := range batch {
			if !dropped[req.ID] {
				resps = append(resps, t.Commit(req.ID, req.Data))
			}
		}
		return resps, nil
	}
}

// Gate returns a step that blocks until gate is closed, then applies inner
// (or commits everything when inner is nil). It simulates a slow attempt
// that stays in flight while retries are dispatched.
func Gate(gate <-chan struct{}, inner StepFunc) StepFunc {
	return func(t *ScriptTransport, batch []Request) ([]Response, error) {
		<-gate
		if inner != nil {
			return inner(t, batch)
		}
		return t.CommitAll(batch), nil
	}
}
