package prefetch

import (
	"context"
	"sync"
)

// Predictor derives the next likely keys from the explicit access
// sequence. It is an injectable function.
type Predictor interface {
	Predict(seq []string) []string
}

// PredictorFunc adapts a plain function to Predictor.
type PredictorFunc func(seq []string) []string

func (f PredictorFunc) Predict(seq []string) []string { return f(seq) }

// Engine records the explicit access sequence, asks its predictors for
// the next keys, and subscribes them as prefetch work. All predictors
// share one per-round budget (maxPredict) and, underneath, the
// scheduler's shared prefetch concurrency budget. Each access advances
// the prediction generation, canceling stale predictions.
type Engine struct {
	sched      *Scheduler
	predictors []Predictor
	maxPredict int

	mu  sync.Mutex
	seq []string
}

func NewEngine(sched *Scheduler, maxPredict int, predictors ...Predictor) *Engine {
	return &Engine{sched: sched, maxPredict: maxPredict, predictors: predictors}
}

// Access serves a real request, records it in the access sequence, then
// re-predicts: the generation advances (stale prefetch subscriptions are
// canceled) and fresh predictions are subscribed under the shared budget.
func (e *Engine) Access(ctx context.Context, key string) (Value, error) {
	v, err := e.sched.Get(ctx, key)
	e.mu.Lock()
	e.seq = append(e.seq, key)
	seq := append([]string(nil), e.seq...)
	e.mu.Unlock()

	gen := e.sched.AdvanceGeneration()
	seen := make(map[string]bool)
	var keys []string
outer:
	for _, p := range e.predictors {
		for _, k := range p.Predict(seq) {
			if len(keys) >= e.maxPredict {
				break outer
			}
			if seen[k] {
				continue
			}
			seen[k] = true
			keys = append(keys, k)
		}
	}
	e.sched.Prefetch(gen, keys...)
	return v, err
}

// Sequence returns a copy of the recorded access sequence.
func (e *Engine) Sequence() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.seq...)
}

// MarkovPredictor is an order-1 predictor: it learns which key followed
// each key in the access sequence and predicts the successor of the last
// accessed key.
type MarkovPredictor struct{}

func (MarkovPredictor) Predict(seq []string) []string {
	if len(seq) < 2 {
		return nil
	}
	last := seq[len(seq)-1]
	for i := len(seq) - 2; i >= 0; i-- {
		if seq[i] == last {
			return []string{seq[i+1]}
		}
	}
	return nil
}
