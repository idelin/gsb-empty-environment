// Package coververify implements anti-forgery verification of state-transition
// coverage reports emitted by a security-testing plugin.
//
// A plugin is not granted coverage merely by declaring that a state was
// visited. Every reported transition must carry the previous-state digest,
// the actual input digest, the produced output and the model version. The
// host replays the transition through a deterministic, model-scoped callback
// and only counts the report when the replayed output matches and the report
// links into the unbroken chain of already accepted transitions.
//
// Reports that cannot link into a chain yet are retained as pending rather
// than counted, and are re-examined after every later submission. Reports
// that contradict an accepted transition, or that try to bind one chain to
// more than one model (cross-model splicing), are isolated as conflicts and
// never counted. Repeated receipts are deduplicated and a failing replay
// callback (error or panic) is never treated as covered.
package coververify

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Digest is a SHA-256 content digest, the on-the-wire summary of a state or
// input. The zero value is reserved and never accepted as evidence.
type Digest [sha256.Size]byte

// String renders the digest in lowercase hexadecimal.
func (d Digest) String() string { return fmt.Sprintf("%x", d[:]) }

// IsZero reports whether d is the reserved zero digest.
func (d Digest) IsZero() bool { return d == Digest{} }

// SumDigest returns the SHA-256 digest of data.
func SumDigest(data []byte) Digest { return sha256.Sum256(data) }

// Status classifies the outcome of verifying one receipt.
type Status int

const (
	// StatusPending means the receipt is internally valid but cannot link
	// into its chain yet. It is retained and retried after later submissions.
	StatusPending Status = iota
	// StatusAccepted means the receipt was replayed successfully and linked
	// into the chain. Only accepted transitions contribute to coverage.
	StatusAccepted
	// StatusRejected means the receipt is malformed, references an unknown
	// chain or model, or fails deterministic replay.
	StatusRejected
	// StatusConflict means the receipt contradicts an accepted transition or
	// splices a second model into a bound chain. It is isolated and never
	// counted.
	StatusConflict
	// StatusDuplicate means the receipt re-reports a transition already
	// recorded. It never inflates coverage.
	StatusDuplicate
	// StatusCallbackError means the deterministic model callback returned an
	// error or panicked while replaying the receipt. It is not covered.
	StatusCallbackError
)

// String returns a stable human-readable name for the status.
func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusAccepted:
		return "accepted"
	case StatusRejected:
		return "rejected"
	case StatusConflict:
		return "conflict"
	case StatusDuplicate:
		return "duplicate"
	case StatusCallbackError:
		return "callback_error"
	default:
		return "unknown"
	}
}

// Receipt is one reported state transition. All evidence fields are
// mandatory; a report missing any is rejected outright.
type Receipt struct {
	// ChainID identifies the independent state-machine chain.
	ChainID string
	// ModelVersion is the model that allegedly produced the transition.
	ModelVersion string
	// PrevStateDigest summarises the state the transition started from.
	PrevStateDigest Digest
	// InputDigest summarises the actual input that was exercised.
	InputDigest Digest
	// Output is the output the plugin claims the model produced.
	Output []byte
	// ReceiptID is an opaque host-assigned bookkeeping identifier. It is not
	// trusted as evidence.
	ReceiptID string
}

// Result is the verification verdict for one receipt.
type Result struct {
	// ReceiptID echoes the verified receipt identifier.
	ReceiptID string
	// Status is the definitive classification of the receipt.
	Status Status
	// Reason is a short human-readable explanation of the verdict.
	Reason string
}

func (r Result) String() string {
	return fmt.Sprintf("coververify: receipt %q: %s: %s", r.ReceiptID, r.Status, r.Reason)
}

// TransitionFunc deterministically recomputes a transition for one model.
// Implementations must be pure: the same state/input pair must always yield
// the same output. A returned error, or a panic, means the report cannot be
// replayed and the receipt does not gain coverage.
type TransitionFunc func(prevState, input Digest) (output []byte, err error)

var (
	// ErrUnknownChain is returned when a receipt names a chain the host
	// never anchored.
	ErrUnknownChain = errors.New("coververify: unknown chain")
	// ErrUnknownModel is returned when a model is referenced before the host
	// registered a replay callback for it.
	ErrUnknownModel = errors.New("coververify: unknown model")
)

// transitionKey identifies a deterministic transition. For a chain bound to
// one model there is exactly one canonical output per key.
type transitionKey struct {
	prev  Digest
	input Digest
}

// pendingEntry is a receipt retained because its chain has a gap. The
// replayed output digest is stored so the entry can be promoted without
// trusting the receipt payload again.
type pendingEntry struct {
	receipt Receipt
	key     transitionKey
	output  Digest
}

type chainState struct {
	anchor   Digest
	model    string
	frontier Digest
	// accepted maps each linked transition key to its canonical output digest.
	accepted map[transitionKey]Digest
	// pending retains unlinked but otherwise valid receipts.
	pending map[transitionKey]pendingEntry
	// conflicts records transition keys isolated on this chain.
	conflicts map[transitionKey]struct{}
}

// Verifier verifies coverage receipts on behalf of a Go test host. It is safe
// for concurrent use.
type Verifier struct {
	mu     sync.Mutex
	models map[string]TransitionFunc
	chains map[string]*chainState

	// Current-classification counters. A retained receipt occupies the
	// pending bucket until its gap closes, then moves to accepted (or is
	// isolated). Re-submitting evidence already held pending does not add a
	// second entry, so the buckets never double-count one transition.
	accepted    int
	pending     int
	rejected    int
	conflict    int
	duplicate   int
	callbackErr int
	conflictLog map[string][]transitionKey
}

// NewVerifier creates an empty Verifier with no models or chains registered.
func NewVerifier() *Verifier {
	return &Verifier{
		models:      make(map[string]TransitionFunc),
		chains:      make(map[string]*chainState),
		conflictLog: make(map[string][]transitionKey),
	}
}

// RegisterModel registers the deterministic replay callback for a model
// version. Re-registering replaces the callback, modelling a deliberate model
// upgrade; transitions accepted under the previous callback remain on record
// and contradictions are later isolated as conflicts.
func (v *Verifier) RegisterModel(version string, fn TransitionFunc) {
	if version == "" || fn == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.models[version] = fn
}

// AnchorChain registers a chain identified by chainID, anchored at the
// genesis-state digest anchor and bound to modelVersion. Every transition on
// the chain must use that model; a later receipt naming another model is
// isolated as a cross-model splice conflict.
func (v *Verifier) AnchorChain(chainID, modelVersion string, anchor Digest) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if chainID == "" {
		return errors.New("coververify: empty chain id")
	}
	if anchor.IsZero() {
		return errors.New("coververify: zero anchor digest")
	}
	if _, ok := v.models[modelVersion]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownModel, modelVersion)
	}
	if _, exists := v.chains[chainID]; exists {
		return fmt.Errorf("coververify: chain %q already anchored", chainID)
	}
	v.chains[chainID] = &chainState{
		anchor:    anchor,
		model:     modelVersion,
		frontier:  anchor,
		accepted:  make(map[transitionKey]Digest),
		pending:   make(map[transitionKey]pendingEntry),
		conflicts: make(map[transitionKey]struct{}),
	}
	return nil
}

// Submit verifies one receipt and updates the coverage accounts. The returned
// Result always carries a definitive Status. A receipt that cannot link yet is
// retained as StatusPending and retried automatically whenever a later
// submission changes the chain.
func (v *Verifier) Submit(r Receipt) Result {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.verifyLocked(r)
}

func (v *Verifier) verifyLocked(r Receipt) Result {
	fail := func(status Status, format string, args ...any) Result {
		v.bump(status)
		return Result{ReceiptID: r.ReceiptID, Status: status, Reason: fmt.Sprintf(format, args...)}
	}

	// 1. Evidence completeness. A bare claim of "state visited" is not enough.
	if r.ChainID == "" {
		return fail(StatusRejected, "empty chain id")
	}
	if r.ModelVersion == "" {
		return fail(StatusRejected, "empty model version")
	}
	if r.PrevStateDigest.IsZero() {
		return fail(StatusRejected, "zero previous-state digest")
	}
	if r.InputDigest.IsZero() {
		return fail(StatusRejected, "zero input digest")
	}
	if len(r.Output) == 0 {
		return fail(StatusRejected, "empty output")
	}

	chain, ok := v.chains[r.ChainID]
	if !ok {
		return fail(StatusRejected, "%s: %q", ErrUnknownChain, r.ChainID)
	}

	// 2. Cross-model splicing: a chain is bound to one model for its lifetime.
	key := transitionKey{prev: r.PrevStateDigest, input: r.InputDigest}
	if chain.model != r.ModelVersion {
		return v.isolateLocked(chain, r, key,
			"cross-model splice: chain %q bound to %q, receipt claims %q",
			r.ChainID, chain.model, r.ModelVersion)
	}
	cb, ok := v.models[r.ModelVersion]
	if !ok {
		return fail(StatusRejected, "%s: %q", ErrUnknownModel, r.ModelVersion)
	}

	// 3. Deterministic replay through the host-registered model callback.
	replayOutput, err := safeCallback(cb, r.PrevStateDigest, r.InputDigest)
	if err != nil {
		return fail(StatusCallbackError, "replay callback failed: %v", err)
	}
	replayDigest := SumDigest(replayOutput)
	if SumDigest(r.Output) != replayDigest {
		return fail(StatusRejected,
			"replay output mismatch: receipt %x, replay %x", SumDigest(r.Output), replayDigest)
	}

	// 4. Conflict isolation against keys already seen on the chain.
	if _, isolated := chain.conflicts[key]; isolated {
		return fail(StatusConflict, "transition key already isolated on chain %q", r.ChainID)
	}
	if canonical, seen := chain.accepted[key]; seen {
		if canonical == replayDigest {
			return fail(StatusDuplicate, "transition already accepted on chain %q", r.ChainID)
		}
		return v.isolateLocked(chain, r, key,
			"output conflict on chain %q: accepted %x, receipt %x",
			r.ChainID, canonical, replayDigest)
	}
	if held, seen := chain.pending[key]; seen {
		if held.output == replayDigest {
			return Result{
				ReceiptID: r.ReceiptID,
				Status:    StatusPending,
				Reason:    "evidence already held pending the chain gap",
			}
		}
		return v.isolateLocked(chain, r, key,
			"output conflict on chain %q against held receipt: held %x, receipt %x",
			r.ChainID, held.output, replayDigest)
	}

	// 5. Linkage. A broken link is retained, never counted.
	if r.PrevStateDigest != chain.frontier {
		chain.pending[key] = pendingEntry{receipt: r, key: key, output: replayDigest}
		v.bump(StatusPending)
		v.resolvePendingLocked(chain)
		return Result{
			ReceiptID: r.ReceiptID,
			Status:    StatusPending,
			Reason: fmt.Sprintf("broken link: prev %x != frontier %x",
				r.PrevStateDigest, chain.frontier),
		}
	}

	// 6. Accept and drain any receipts whose gap has just closed.
	v.acceptLocked(chain, key, replayDigest)
	resolved := v.resolvePendingLocked(chain)
	reason := fmt.Sprintf("accepted on chain %q", r.ChainID)
	if resolved > 0 {
		reason += fmt.Sprintf("; %d retained receipt(s) now linked", resolved)
	}
	return Result{ReceiptID: r.ReceiptID, Status: StatusAccepted, Reason: reason}
}

// safeCallback invokes cb, converting a panic into an ordinary error so a
// misbehaving model can never crash the host and never earns coverage.
func safeCallback(cb TransitionFunc, prev, input Digest) (output []byte, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("callback panic: %v", rec)
		}
	}()
	return cb(prev, input)
}

func (v *Verifier) acceptLocked(c *chainState, key transitionKey, outputDigest Digest) {
	c.accepted[key] = outputDigest
	c.frontier = outputDigest
	v.accepted++
}

// isolateLocked quarantines a contradictory key, drops any retained entry for
// it, and records the conflict for diagnostics.
func (v *Verifier) isolateLocked(c *chainState, r Receipt, key transitionKey,
	format string, args ...any) Result {
	c.conflicts[key] = struct{}{}
	delete(c.pending, key)
	v.conflictLog[r.ChainID] = append(v.conflictLog[r.ChainID], key)
	v.bump(StatusConflict)
	return Result{ReceiptID: r.ReceiptID, Status: StatusConflict, Reason: fmt.Sprintf(format, args...)}
}

// resolvePendingLocked promotes retained receipts in deterministic order once
// their previous state reaches the chain frontier. Held evidence is replayed
// again before promotion so a replaced callback cannot launder a stale or
// contradictory receipt into coverage.
func (v *Verifier) resolvePendingLocked(c *chainState) int {
	resolved := 0
	for {
		keys := make([]transitionKey, 0, len(c.pending))
		for k := range c.pending {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return digestKeyLess(keys[i], keys[j]) })

		progressed := false
		for _, k := range keys {
			entry := c.pending[k]
			if entry.receipt.PrevStateDigest != c.frontier {
				continue
			}
			cb := v.models[c.model]
			replayOutput, err := safeCallback(cb, k.prev, k.input)
			switch {
			case err != nil:
				// The callback can no longer reproduce the held receipt: it
				// does not count, but stays retained rather than being laundered.
				continue
			case SumDigest(replayOutput) != entry.output:
				v.isolateLocked(c, entry.receipt, k,
					"held receipt contradicted on replay: held %x, replay %x",
					entry.output, SumDigest(replayOutput))
				v.pending--
				progressed = true
			default:
				delete(c.pending, k)
				v.pending--
				v.acceptLocked(c, k, entry.output)
				resolved++
				progressed = true
			}
		}
		if !progressed {
			return resolved
		}
	}
}

func digestKeyLess(a, b transitionKey) bool {
	if c := digestCmp(a.prev, b.prev); c != 0 {
		return c < 0
	}
	return digestCmp(a.input, b.input) < 0
}

func digestCmp(a, b Digest) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (v *Verifier) bump(s Status) {
	switch s {
	case StatusAccepted:
		v.accepted++
	case StatusPending:
		v.pending++
	case StatusRejected:
		v.rejected++
	case StatusConflict:
		v.conflict++
	case StatusDuplicate:
		v.duplicate++
	case StatusCallbackError:
		v.callbackErr++
	}
}

// Stats is a point-in-time snapshot of the coverage accounts. The sum of all
// classification counters always equals Total, so the host can prove that no
// submitted receipt vanished or was silently counted.
type Stats struct {
	Total         int
	Accepted      int
	Pending       int
	Rejected      int
	Conflict      int
	Duplicate     int
	CallbackError int
}

// Stats returns a snapshot of the current coverage accounts.
func (v *Verifier) Stats() Stats {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Stats{
		Total:         v.accepted + v.pending + v.rejected + v.conflict + v.duplicate + v.callbackErr,
		Accepted:      v.accepted,
		Pending:       v.pending,
		Rejected:      v.rejected,
		Conflict:      v.conflict,
		Duplicate:     v.duplicate,
		CallbackError: v.callbackErr,
	}
}

// Pending returns the number of retained receipts still waiting on a chain gap.
func (v *Verifier) Pending() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.pending
}

// Covered returns the number of replay-verified, linked transitions. Only
// these count as coverage.
func (v *Verifier) Covered() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.accepted
}

// Frontier returns the current frontier digest of chainID, or the zero digest
// and false when the chain is unknown.
func (v *Verifier) Frontier(chainID string) (Digest, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.chains[chainID]
	if !ok {
		return Digest{}, false
	}
	return c.frontier, true
}

// ChainModel reports the model version a chain is bound to, or false for an
// unknown chain.
func (v *Verifier) ChainModel(chainID string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.chains[chainID]
	if !ok {
		return "", false
	}
	return c.model, true
}

// IsConflict reports whether the transition identified by prev/input on
// chainID has been isolated.
func (v *Verifier) IsConflict(chainID string, prev, input Digest) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.chains[chainID]
	if !ok {
		return false
	}
	_, isolated := c.conflicts[transitionKey{prev: prev, input: input}]
	return isolated
}
