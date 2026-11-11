package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func targetClass() string { return errFakeHandshake.Error() }

func assertIDs(t *testing.T, got []Message, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("trace length = %d, want ids %v (got %v)", len(got), want, messageIDs(got))
	}
	for i, msg := range got {
		if msg.ID != want[i] {
			t.Fatalf("trace ids = %v, want %v", messageIDs(got), want)
		}
	}
}

// TestCascadeAcrossConnections verifies that deleting a ticket issued on c1
// cascades to every consumer on c2 as well, and that the minimal trace still
// fails with the exact same handshake class.
func TestCascadeAcrossConnections(t *testing.T) {
	harness := newFakeHarness(fakeConfig{})
	result := MinimizeT(t, harness, fullTrace(), Options{Classify: classify})

	assertMinimal(t, harness, result)
	if len(result.Messages) > 4 {
		t.Fatalf("minimized to %d messages, expected at most 4", len(result.Messages))
	}
	if result.TargetFailure != targetClass() {
		t.Fatalf("target failure = %q", result.TargetFailure)
	}
	// The surviving handshake still consumes the ticket produced on c1, so a
	// cross-connection dependency survives in every minimal trace.
	assertCrossConnectionTicket(t, result.Messages)

	// Every audit probe must be non-removable: removing any closure changes
	// the failure class or removes the handshake.
	if len(result.ClosureChecks) != len(result.Messages) {
		t.Fatalf("closure checks = %d, want %d", len(result.ClosureChecks), len(result.Messages))
	}
	for _, check := range result.ClosureChecks {
		if check.StillRemovable {
			t.Fatalf("closure %v still removable at fixpoint", check.Removed)
		}
	}

	// Some accepted run must have deleted the c1 ticket producer (id 1) and
	// the c2 handshake consumer (id 9) in the same cascade step.
	if !acceptedRunRemoved(result, 1, 9) {
		t.Fatalf("no accepted cascade step removed c1 producer 1 together with c2 consumer 9: %+v",
			result.Runs)
	}
}

func acceptedRunRemoved(result Result, ids ...int) bool {
	previous := map[int]bool{}
	for _, id := range result.Runs[0].Kept {
		previous[id] = true
	}
	for _, run := range result.Runs[1:] {
		current := map[int]bool{}
		for _, id := range run.Kept {
			current[id] = true
		}
		if run.Accepted {
			all := true
			for _, id := range ids {
				if previous[id] && current[id] {
					all = false
				}
			}
			if all {
				return true
			}
		}
		previous = current
	}
	return false
}

func assertMinimal(t *testing.T, harness *fakeHarness, result Result) {
	t.Helper()
	if len(result.ClosureChecks) != len(result.Messages) {
		t.Fatalf("closure checks = %d, want %d", len(result.ClosureChecks), len(result.Messages))
	}
	for _, check := range result.ClosureChecks {
		if check.StillRemovable {
			t.Fatalf("closure %v still removable at fixpoint", check.Removed)
		}
	}
}

func assertCrossConnectionTicket(t *testing.T, msgs []Message) {
	t.Helper()
	producerConn, consumerConn := "", ""
	for _, msg := range msgs {
		for _, sym := range msg.Produces {
			if sym == symTicket {
				producerConn = msg.Conn
			}
		}
		if msg.Kind == kindHandshake {
			consumerConn = msg.Conn
		}
	}
	if producerConn == "" || consumerConn == "" {
		t.Fatalf("minimal trace lost ticket producer or handshake: %v", messageIDs(msgs))
	}
	if producerConn == consumerConn {
		t.Fatalf("ticket no longer crosses connections (%s -> %s)", producerConn, consumerConn)
	}
}

// TestDuplicateConsumes verifies repeated references to a shared symbol do not
// confuse dependency tracking: message 8 consumes the ticket twice and is
// removable as noise while the genuine handshake consumers stay valid.
func TestDuplicateConsumes(t *testing.T) {
	harness := newFakeHarness(fakeConfig{})
	result := MinimizeT(t, harness, fullTrace(), Options{Classify: classify})

	if containsID(result.Messages, 7) {
		t.Fatalf("duplicate-consume noise message 7 survived minimization")
	}

	// Replay the result on yet another fresh session: duplicate or not, every
	// surviving consume must resolve inside that one session.
	session, err := harness.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Run(t.Context(), result.Messages); !errors.Is(err, errFakeHandshake) {
		t.Fatalf("minimized replay err = %v, want handshake failure", err)
	}
}

// TestDuplicateProducers makes two messages produce the same ticket symbol.
// Deleting one producer must not cascade while another producer still
// satisfies later consumers.
func TestDuplicateProducers(t *testing.T) {
	trace := []Message{
		{ID: 0, Conn: "c1", Kind: kindIssue, Produces: []SymID{symTicket}},
		{ID: 1, Conn: "c1", Kind: kindIssue, Produces: []SymID{symTicket}},
		{ID: 2, Conn: "c2", Kind: kindHandshake, Consumes: []SymID{symTicket}},
	}
	harness := newFakeHarness(fakeConfig{})
	result := MinimizeT(t, harness, trace, Options{Classify: classify})

	if len(result.Messages) != 2 || result.Messages[1].ID != 2 {
		t.Fatalf("minimal trace = %v, want one producer plus handshake", messageIDs(result.Messages))
	}

	// The recorded run that dropped one producer kept the handshake failure
	// class, proving the remaining duplicate producer was sufficient.
	found := false
	for _, run := range result.Runs {
		if len(run.Kept) == 2 && run.Kept[1] == 2 && run.Accepted {
			found = true
		}
	}
	if !found {
		t.Fatalf("no accepted run relied on the surviving duplicate producer: %+v", result.Runs)
	}
}

// TestNonMonotoneClassification uses a harness where over-aggressive deletion
// changes the failure class. Such candidates must be rejected even though
// they still "fail"; only same-class failures are accepted.
func TestNonMonotoneClassification(t *testing.T) {
	harness := newFakeHarness(fakeConfig{nonMonotoneAt: 4})
	result := MinimizeT(t, harness, fullTrace(), Options{Classify: classify})

	assertMinimal(t, harness, result)
	if len(result.Messages) < 4 {
		t.Fatalf("non-monotone trace shrank below threshold to %d messages", len(result.Messages))
	}
	for _, run := range result.Runs {
		if run.Accepted && run.Failure != targetClass() {
			t.Fatalf("accepted run with foreign class %q: %+v", run.Failure, run)
		}
	}

	// A three-message candidate triggers the distinct non-monotone class and
	// must appear as a rejected probe, not as the final trace.
	rejected := false
	for _, run := range result.Runs {
		if run.Failure == errFakeNonMono.Error() && !run.Accepted {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("non-monotone class was never observed and rejected")
	}
}

// TestTimeoutIsDifferentClass verifies caller-provided timeout handling: once
// the trace gets short enough to time out, the timeout class differs from the
// handshake class and the candidate is rejected, leaving the fixpoint above
// the timeout threshold.
func TestTimeoutIsDifferentClass(t *testing.T) {
	harness := newFakeHarness(fakeConfig{timeoutAt: 4})
	result := MinimizeT(t, harness, fullTrace(), Options{Classify: classify})

	assertMinimal(t, harness, result)
	if len(result.Messages) <= 4 {
		t.Fatalf("timeout threshold not respected: %d messages remain", len(result.Messages))
	}
	sawTimeout := false
	for _, run := range result.Runs {
		if run.TimedOut {
			sawTimeout = true
			if run.Accepted {
				t.Fatalf("timeout probe was accepted: %+v", run)
			}
			if run.Failure != "TIMEOUT" {
				t.Fatalf("timeout class = %q", run.Failure)
			}
		}
	}
	if !sawTimeout {
		t.Fatalf("harness never produced a timeout probe")
	}
}

// TestFreshSessionPerRun asserts structurally that no candidate can reuse
// server state: each evaluation gets a unique generation, sessions are used
// for a single run and closed, and the fake server itself rejects any ticket
// whose issuing generation differs from the running session.
func TestFreshSessionPerRun(t *testing.T) {
	harness := newFakeHarness(fakeConfig{})
	result := MinimizeT(t, harness, fullTrace(), Options{Classify: classify})

	if len(result.Runs) != harness.generationCount() {
		t.Fatalf("runs=%d sessions=%d, want one session per run",
			len(result.Runs), harness.generationCount())
	}

	seen := map[uint64]bool{}
	for _, run := range result.Runs {
		if seen[run.Generation] {
			t.Fatalf("generation %d reused across runs", run.Generation)
		}
		seen[run.Generation] = true

		session := harness.sessions[run.Generation]
		if session == nil {
			t.Fatalf("missing session for generation %d", run.Generation)
		}
		if session.runs != 1 {
			t.Fatalf("session %d executed %d runs, want exactly 1", run.Generation, session.runs)
		}
		if !session.closed {
			t.Fatalf("session %d was not closed", run.Generation)
		}
	}

	// No run may ever observe the stale-reuse error class.
	for _, run := range result.Runs {
		if strings.Contains(run.Failure, errFakeReuse.Error()) {
			t.Fatalf("run on generation %d reused prior server state", run.Generation)
		}
	}
}

// TestRepairPolicy keeps dependent consumers alive and rewrites their dangling
// ticket consumption instead of cascading.
func TestRepairPolicy(t *testing.T) {
	trace := []Message{
		{ID: 0, Conn: "c1", Kind: kindConnect, Produces: []SymID{symNonce}},
		{ID: 1, Conn: "c1", Kind: kindIssue, Consumes: []SymID{symNonce}, Produces: []SymID{symTicket}},
		{ID: 2, Conn: "c2", Kind: kindAuth, Consumes: []SymID{symTicket}, Produces: []SymID{symSession}},
		{ID: 3, Conn: "c2", Kind: kindHandshake, Consumes: []SymID{symTicket, symSession}},
	}
	harness := newFakeHarness(fakeConfig{})
	result := MinimizeT(t, harness, trace, Options{
		Policy:   Repair,
		Repair:   rewriteTicketRepair,
		Classify: classify,
	})

	// The only ticket producer is deleted while both c2 consumers are repaired
	// in place instead of being cascaded away.
	assertIDs(t, result.Messages, []int{2, 3})

	// The repaired handshake keeps its session consumption but drops the
	// dangling ticket consumption.
	handshake := result.Messages[1]
	if containsSym(handshake.Consumes, symTicket) {
		t.Fatalf("dangling ticket not repaired: %+v", handshake)
	}
	if !containsSym(handshake.Consumes, symSession) {
		t.Fatalf("valid session dependency was dropped: %+v", handshake)
	}

	// Repaired messages must be copies: the original trace keeps its consumes.
	if len(trace[3].Consumes) != 2 || trace[3].Consumes[0] != symTicket {
		t.Fatalf("original message was mutated by repair: %+v", trace[3])
	}

	// Every closure audit under repair probes exactly one root id.
	for _, check := range result.ClosureChecks {
		if len(check.Removed) != 1 {
			t.Fatalf("repair audit removed closure %v, want single root", check.Removed)
		}
	}
}

func containsID(msgs []Message, id int) bool {
	for _, msg := range msgs {
		if msg.ID == id {
			return true
		}
	}
	return false
}

func containsSym(syms []SymID, want SymID) bool {
	for _, sym := range syms {
		if sym == want {
			return true
		}
	}
	return false
}

// TestRepairRejection verifies that a repair func rejecting a dependency makes
// the candidate invalid rather than silently dropping a consumer.
func TestRepairRejection(t *testing.T) {
	trace := []Message{
		{ID: 0, Conn: "c1", Kind: kindIssue, Produces: []SymID{symTicket}},
		{ID: 1, Conn: "c2", Kind: kindHandshake, Consumes: []SymID{symTicket}},
	}
	harness := newFakeHarness(fakeConfig{})
	reject := func(Message, []SymID) (Message, bool) { return Message{}, false }
	result := MinimizeT(t, harness, trace, Options{
		Policy:   Repair,
		Repair:   reject,
		Classify: classify,
	})

	assertIDs(t, result.Messages, []int{0, 1})
}

// TestBaselineNotFailing ensures reduction refuses a trace that does not fail.
func TestBaselineNotFailing(t *testing.T) {
	trace := []Message{
		{ID: 0, Conn: "c1", Kind: kindConnect, Produces: []SymID{symNonce}},
		{ID: 1, Conn: "c1", Kind: kindNoise, Consumes: []SymID{symNonce}},
	}
	harness := newFakeHarness(fakeConfig{})
	_, err := Minimize(harness, trace, Options{Classify: classify})
	if err == nil {
		t.Fatalf("Minimize succeeded on a non-failing trace")
	}
}

// TestMinimizeTErrorPath exercises the testing.T integration when no
// classifier-compatible failure exists.
func TestMinimizeTErrorPath(t *testing.T) {
	harness := newFakeHarness(fakeConfig{})
	recorder := &recordingT{}
	result := MinimizeT(recorder, harness, []Message{{ID: 0, Conn: "c", Kind: kindConnect}}, Options{
		Classify: classify,
	})
	if !recorder.failed {
		t.Fatalf("MinimizeT did not fail on empty/non-failing input")
	}
	if len(result.Messages) != 0 {
		t.Fatalf("expected zero value result after failure")
	}
}

type recordingT struct {
	logs   []string
	failed bool
}

func (r *recordingT) Helper() {}
func (r *recordingT) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}
func (r *recordingT) Errorf(format string, args ...any) {
	r.failed = true
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}
