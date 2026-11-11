package netsec

import (
	"context"
	"errors"
	"fmt"
)

// Policy selects how a deletion that breaks a producer/consumer edge is
// resolved.
type Policy int

const (
	// Cascade deletes every dependent message together with the deletion
	// root, so every surviving consumer keeps at least one live producer.
	Cascade Policy = iota

	// Repair keeps the dependent messages and hands each message with a
	// dangling consumption to Options.Repair. If Repair rejects a message the
	// whole candidate is rejected.
	Repair
)

// TimeoutError marks a failure caused by the caller deadline rather than an
// assertion. A classifier may also map any error to a timeout class itself.
var TimeoutError = errors.New("netsec: candidate timed out")

// Classifier maps a run error to an opaque failure class. Candidates are only
// accepted when the class equals the baseline class, so a classifier must
// return distinct strings for distinct root causes and a stable string for
// the same root cause. Returning an empty error class means the candidate did
// not fail.
type Classifier func(err error) (failureClass string, timedOut bool)

// DefaultClassifier treats TimeoutError (including context deadline exceeded
// wrapped by a harness) as a timeout and uses the error text otherwise.
func DefaultClassifier(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, TimeoutError), errors.Is(err, context.DeadlineExceeded):
		return "timeout", true
	default:
		return err.Error(), false
	}
}

// Session is one isolated server instance/state. A Session is used for exactly
// one candidate and then closed, which structurally prevents candidates from
// reusing server state produced by an earlier candidate.
type Session interface {
	// Generation is a unique, monotonically increasing session identifier.
	Generation() uint64

	// Run replays msgs against this fresh session. It must honor ctx
	// cancellation and wrap deadline failures in TimeoutError or
	// context.DeadlineExceeded.
	Run(ctx context.Context, msgs []Message) error

	// Close releases all resources owned by the session.
	Close() error
}

// Harness builds isolated sessions. Failure classification and timeout
// handling stay entirely on the caller side through the Classifier.
type Harness interface {
	NewSession() (Session, error)
}

// Options configures Minimize.
type Options struct {
	// Context bounds every candidate run. A canceled parent cancels ongoing
	// reduction. Defaults to context.Background.
	Context context.Context

	// Policy is Cascade or Repair. Defaults to Cascade.
	Policy Policy

	// Repair is required for the Repair policy and ignored otherwise.
	Repair RepairFunc

	// Classify maps run errors to failure classes. Defaults to
	// DefaultClassifier.
	Classify Classifier

	// MaxRuns caps the number of candidate sessions including the baseline.
	// Zero means a generous built-in default; a negative value means no cap.
	MaxRuns int

	// Logger, when non-nil, receives a line per evaluated candidate.
	Logger interface {
		Logf(format string, args ...any)
	}
}

const defaultMaxRuns = 10000

// Minimize shrinks trace until no deletable closure can be removed without
// changing the failure class. Every candidate is replayed against a brand new
// Harness session. It returns an error when the baseline trace does not fail
// or reduction is interrupted before a fixpoint is reached.
func Minimize(harness Harness, trace []Message, opts Options) (Result, error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	classify := opts.Classify
	if classify == nil {
		classify = DefaultClassifier
	}
	if opts.Policy == Repair && opts.Repair == nil {
		return Result{}, errors.New("netsec: Repair policy requires Options.Repair")
	}
	maxRuns := opts.MaxRuns
	if maxRuns == 0 {
		maxRuns = defaultMaxRuns
	}

	m := &minimizer{
		harness:  harness,
		opts:     opts,
		ctx:      ctx,
		classify: classify,
		maxRuns:  maxRuns,
	}

	baseline, err := m.evaluate(trace, false)
	if err != nil {
		return Result{}, err
	}
	if baseline.Failure == "" {
		return Result{}, errors.New("netsec: baseline trace does not fail; nothing to reduce")
	}

	current := append([]Message(nil), trace...)
	target := baseline.Failure

	for {
		shrunk, err := m.shrinkPass(current, target)
		if err != nil {
			return Result{}, err
		}
		current = shrunk

		checks, next, done, err := m.audit(current, target)
		if err != nil {
			return Result{}, err
		}
		if done {
			return Result{
				Messages:      current,
				TargetFailure: target,
				Runs:          m.runs,
				ClosureChecks: checks,
			}, nil
		}
		current = next
	}
}

type minimizer struct {
	harness  Harness
	opts     Options
	ctx      context.Context
	classify Classifier
	maxRuns  int
	runs     []Run
	used     int
}

// evaluate opens a fresh session, replays candidate once and records the run.
func (m *minimizer) evaluate(candidate []Message, accepted bool) (Run, error) {
	if m.maxRuns > 0 && m.used >= m.maxRuns {
		return Run{}, fmt.Errorf("netsec: MaxRuns %d exhausted before reaching a fixpoint", m.maxRuns)
	}
	if err := m.ctx.Err(); err != nil {
		return Run{}, fmt.Errorf("netsec: reduction canceled: %w", err)
	}

	session, err := m.harness.NewSession()
	if err != nil {
		return Run{}, fmt.Errorf("netsec: new session: %w", err)
	}
	m.used++

	runErr := session.Run(m.ctx, candidate)
	closeErr := session.Close()
	if runErr == nil && closeErr != nil {
		runErr = closeErr
	}

	failure, timedOut := m.classify(runErr)
	record := Run{
		Generation: session.Generation(),
		Kept:       messageIDs(candidate),
		Failure:    failure,
		TimedOut:   timedOut,
		Accepted:   accepted,
	}
	m.runs = append(m.runs, record)
	if m.opts.Logger != nil {
		m.opts.Logger.Logf("session=%d kept=%v failure=%q timedOut=%t",
			record.Generation, record.Kept, failure, timedOut)
	}
	return record, nil
}

// shrinkPass walks every currently deletable closure once and accepts each
// deletion that preserves the target failure class.
func (m *minimizer) shrinkPass(msgs []Message, target string) ([]Message, error) {
	current := msgs
	for {
		idx := buildIndex(current)
		alive := aliveSet(current)
		progressed := false

		for _, rootMsg := range current {
			root := rootMsg.ID
			candidate, _, ok := m.candidateFor(current, idx, alive, root)
			if !ok || len(candidate) == len(current) {
				continue
			}
			record, err := m.evaluate(candidate, false)
			if err != nil {
				return nil, err
			}
			if record.Failure == target {
				record.Accepted = true
				m.runs[len(m.runs)-1] = record
				current = candidate
				progressed = true
				break
			}
		}

		if !progressed {
			return current, nil
		}
	}
}

// candidateFor builds the deletion candidate rooted at root under the policy.
func (m *minimizer) candidateFor(msgs []Message, idx depIndex, alive map[int]bool, root int) (candidate []Message, removed []int, ok bool) {
	switch m.opts.Policy {
	case Repair:
		candidate, ok := buildRepairCandidate(msgs, idx, alive, []int{root}, m.opts.Repair)
		return candidate, []int{root}, ok
	default:
		closed := closureOfOne(idx, alive, root)
		return buildCascadeCandidate(msgs, idx, alive, []int{root}), sortedIDs(idx, closed), true
	}
}

// audit probes every deletable closure of the current trace. It returns
// done=true only when every probe fails with a different class (i.e. nothing
// is removable). A removable probe is accepted immediately and returned as
// next so shrinking restarts; this also covers nondeterministic or
// non-monotone classifications.
func (m *minimizer) audit(msgs []Message, target string) (checks []ClosureCheck, next []Message, done bool, err error) {
	idx := buildIndex(msgs)
	alive := aliveSet(msgs)

	for _, rootMsg := range msgs {
		root := rootMsg.ID
		var removed []int
		if m.opts.Policy == Repair {
			removed = []int{root}
		} else {
			removed = sortedIDs(idx, closureOfOne(idx, alive, root))
		}

		candidate, _, ok := m.candidateFor(msgs, idx, alive, root)
		if !ok {
			continue
		}
		record, err := m.evaluate(candidate, false)
		if err != nil {
			return nil, nil, false, err
		}
		stillRemovable := record.Failure == target
		checks = append(checks, ClosureCheck{
			Removed:        removed,
			Failure:        record.Failure,
			TimedOut:       record.TimedOut,
			StillRemovable: stillRemovable,
		})
		if stillRemovable {
			record.Accepted = true
			m.runs[len(m.runs)-1] = record
			return checks, candidate, false, nil
		}
	}
	return checks, nil, true, nil
}

func messageIDs(msgs []Message) []int {
	ids := make([]int, len(msgs))
	for i, msg := range msgs {
		ids[i] = msg.ID
	}
	return ids
}

// MinimizeT runs Minimize and fails t on error with a readable message.
func MinimizeT(t T, harness Harness, trace []Message, opts Options) Result {
	t.Helper()
	result, err := Minimize(harness, trace, opts)
	if err != nil {
		t.Errorf("netsec: sequence reduction failed: %v", err)
		return Result{}
	}
	t.Logf("netsec: reduced %d messages to %d (target failure %q)",
		len(trace), len(result.Messages), result.TargetFailure)
	return result
}
