// Package scandetect implements per-source port-scan detection with a
// Threshold Random Walk (TRW), i.e. a sequential probability ratio test
// (SPRT) on connection success/failure outcomes.
//
// For every source the detector maintains a log-likelihood ratio comparing
//
//	H1: the source is a scanner   (low connection success probability theta1)
//	H0: the source is benign      (high connection success probability theta0)
//
// Only the first connection to a given (source, target) pair updates the
// statistic: repeated connections to an already-seen target are ignored, so
// a benign client talking to many services or retrying the same service does
// not accumulate evidence against itself.
//
// The number of tracked source states is bounded by MaxSources (LRU
// eviction) and each per-source target set is bounded by
// MaxTargetsPerSource, giving an overall O(MaxSources*MaxTargetsPerSource)
// memory bound. An evicted source starts from scratch if it reappears.
package scandetect

import (
	"container/list"
	"errors"
	"math"
	"sync"
)

// Verdict is the current decision for a tracked source.
type Verdict int8

const (
	// Pending means neither threshold has been crossed yet.
	Pending Verdict = iota
	// Scanner means the upper threshold has been crossed.
	Scanner
	// Benign means the lower threshold has been crossed.
	Benign
)

func (v Verdict) String() string {
	switch v {
	case Scanner:
		return "scanner"
	case Benign:
		return "benign"
	default:
		return "pending"
	}
}

// Config configures the TRW/SPRT detector.
type Config struct {
	// SuccessProbBenign is P(connection succeeds | benign source), theta0.
	SuccessProbBenign float64
	// SuccessProbScanner is P(connection succeeds | scanner), theta1.
	// It must be strictly smaller than SuccessProbBenign.
	SuccessProbScanner float64
	// FalsePositiveRate (alpha) bounds P(declare scanner | benign).
	FalsePositiveRate float64
	// FalseNegativeRate (beta) bounds P(declare benign | scanner).
	FalseNegativeRate float64
	// MaxSources is the hard cap on concurrently tracked source states.
	// When the cap is reached the least-recently-used source is evicted.
	MaxSources int
	// MaxTargetsPerSource caps the per-source dedup set. Zero disables the
	// cap. When the cap is reached the oldest target entry is forgotten, so
	// a later connection to that target is counted as new again.
	MaxTargetsPerSource int
}

// DefaultConfig returns the classic TRW parameters (theta0=0.8, theta1=0.2)
// with 1% nominal error rates.
func DefaultConfig() Config {
	return Config{
		SuccessProbBenign:   0.8,
		SuccessProbScanner:  0.2,
		FalsePositiveRate:   0.01,
		FalseNegativeRate:   0.01,
		MaxSources:          100_000,
		MaxTargetsPerSource: 4_096,
	}
}

// SourceStats is a point-in-time snapshot of a source's state.
type SourceStats struct {
	Attempts           int
	UniqueConnections  int
	Successes          int
	Failures           int
	LogLikelihoodRatio float64
	Verdict            Verdict
}

type sourceState struct {
	key         string
	targets     map[string]struct{}
	targetQueue []string
	queueHead   int
	llr         float64
	attempts    int
	successes   int
	failures    int
	verdict     Verdict
	elem        *list.Element
}

// Detector runs per-source sequential probability ratio tests. It is safe
// for concurrent use.
type Detector struct {
	mu sync.Mutex

	cfg         Config
	upper       float64
	lower       float64
	stepSuccess float64
	stepFailure float64

	lru       *list.List
	index     map[string]*list.Element
	evictions int64
}

// New builds a Detector and validates the configuration.
func New(cfg Config) (*Detector, error) {
	if cfg.SuccessProbBenign <= 0 || cfg.SuccessProbBenign >= 1 {
		return nil, errors.New("scandetect: SuccessProbBenign must be in (0,1)")
	}
	if cfg.SuccessProbScanner <= 0 || cfg.SuccessProbScanner >= 1 {
		return nil, errors.New("scandetect: SuccessProbScanner must be in (0,1)")
	}
	if cfg.SuccessProbScanner >= cfg.SuccessProbBenign {
		return nil, errors.New("scandetect: SuccessProbScanner must be smaller than SuccessProbBenign")
	}
	if cfg.FalsePositiveRate <= 0 || cfg.FalsePositiveRate >= 1 {
		return nil, errors.New("scandetect: FalsePositiveRate must be in (0,1)")
	}
	if cfg.FalseNegativeRate <= 0 || cfg.FalseNegativeRate >= 1 {
		return nil, errors.New("scandetect: FalseNegativeRate must be in (0,1)")
	}
	if cfg.MaxSources <= 0 {
		return nil, errors.New("scandetect: MaxSources must be positive")
	}
	if cfg.MaxTargetsPerSource < 0 {
		return nil, errors.New("scandetect: MaxTargetsPerSource must be non-negative")
	}
	return &Detector{
		cfg: cfg,
		// ln((1-beta)/alpha): declare scanner above this.
		upper: math.Log((1 - cfg.FalseNegativeRate) / cfg.FalsePositiveRate),
		// ln(beta/(1-alpha)): declare benign below this.
		lower:       math.Log(cfg.FalseNegativeRate / (1 - cfg.FalsePositiveRate)),
		stepSuccess: math.Log(cfg.SuccessProbScanner / cfg.SuccessProbBenign),
		stepFailure: math.Log((1 - cfg.SuccessProbScanner) / (1 - cfg.SuccessProbBenign)),
		lru:         list.New(),
		index:       make(map[string]*list.Element),
	}, nil
}

// Observe feeds one connection outcome. target should identify the remote
// endpoint, typically "ip:port". Repeated connections to a target already
// seen for this source do not update the likelihood ratio. The returned
// Verdict is the source's verdict after processing the event.
//
// A verdict, once reached, stays frozen for the lifetime of the source
// state; evicting the source (see MaxSources) resets it completely.
func (d *Detector) Observe(source, target string, success bool) Verdict {
	d.mu.Lock()
	defer d.mu.Unlock()

	el, ok := d.index[source]
	if !ok {
		if d.lru.Len() >= d.cfg.MaxSources {
			d.evict(d.lru.Back())
		}
		st := &sourceState{
			key:     source,
			targets: make(map[string]struct{}),
			verdict: Pending,
		}
		el = d.lru.PushFront(st)
		st.elem = el
		d.index[source] = el
	} else {
		d.lru.MoveToFront(el)
	}
	st := el.Value.(*sourceState)
	st.attempts++

	if st.verdict != Pending {
		return st.verdict
	}
	if _, seen := st.targets[target]; seen {
		// Duplicate connection to an already-seen target: count it only once.
		return Pending
	}

	if limit := d.cfg.MaxTargetsPerSource; limit > 0 && len(st.targets) >= limit {
		oldest := st.targetQueue[st.queueHead]
		st.queueHead++
		if st.queueHead == len(st.targetQueue) {
			st.targetQueue = st.targetQueue[:0]
			st.queueHead = 0
		}
		delete(st.targets, oldest)
	}
	st.targets[target] = struct{}{}
	st.targetQueue = append(st.targetQueue, target)

	if success {
		st.llr += d.stepSuccess
		st.successes++
	} else {
		st.llr += d.stepFailure
		st.failures++
	}

	switch {
	case st.llr >= d.upper:
		st.verdict = Scanner
	case st.llr <= d.lower:
		st.verdict = Benign
	}
	return st.verdict
}

func (d *Detector) evict(el *list.Element) {
	st := el.Value.(*sourceState)
	d.lru.Remove(el)
	delete(d.index, st.key)
	d.evictions++
}

// Stats returns a snapshot for the given source and whether it is tracked.
func (d *Detector) Stats(source string) (SourceStats, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	el, ok := d.index[source]
	if !ok {
		return SourceStats{}, false
	}
	st := el.Value.(*sourceState)
	return SourceStats{
		Attempts:           st.attempts,
		UniqueConnections:  len(st.targets),
		Successes:          st.successes,
		Failures:           st.failures,
		LogLikelihoodRatio: st.llr,
		Verdict:            st.verdict,
	}, true
}

// Len returns the number of currently tracked sources.
func (d *Detector) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lru.Len()
}

// Evictions returns the cumulative number of source states dropped under
// the MaxSources memory cap.
func (d *Detector) Evictions() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.evictions
}

// Thresholds returns the natural-log upper (scanner) and lower (benign) bounds.
func (d *Detector) Thresholds() (upper, lower float64) {
	return d.upper, d.lower
}
