package budget

import (
	"context"
	"sort"
	"sync"
)

// group is a minimal stdlib-only errgroup: it runs goroutines under a
// cancellable context and retains the first non-nil error.
type group struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg  sync.WaitGroup
	mu  sync.Mutex
	err error
}

func newGroup(ctx context.Context) *group {
	gctx, cancel := context.WithCancel(ctx)
	return &group{ctx: gctx, cancel: cancel}
}

func (g *group) goFn(fn func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := fn(); err != nil {
			g.mu.Lock()
			if g.err == nil {
				g.err = err
			}
			g.mu.Unlock()
			g.cancel()
		}
	}()
}

func (g *group) wait() error {
	g.wg.Wait()
	g.cancel()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

// Unfold validates and decodes root under the four budgets in limits. It
// returns a published Result only when every node was visited and every leaf
// decoded without violating a budget. On any failure it returns a nil Result,
// a non-nil error and guarantees that every staged buffer was reclaimed and
// nothing was published.
//
// The context may be cancelled by the caller to abort an in-flight unfold;
// cancellation is also triggered internally the moment a budget is violated.
func Unfold(ctx context.Context, limits Limits, root *Node) (*Result, error) {
	return unfold(ctx, limits, root, unfoldConfig{})
}

// unfoldConfig carries seams used by the package's own fault-injection tests.
// The zero value selects the production byte-buffer sink and direct return.
type unfoldConfig struct {
	// newSink builds the staging sink for each leaf reference.
	newSink func() sink

	// publish, when non-nil, is the single publication point. It runs exactly
	// once and only after the whole tree succeeded; an error here is treated as
	// a failed unfold with every buffer reclaimed.
	publish func(context.Context, *Result) error
}

type leafJob struct {
	node  *Node
	path  []string
	index int
}

type stagedLeaf struct {
	leaf  Leaf
	sink  sink
	index int
}

func unfold(ctx context.Context, limits Limits, root *Node, cfg unfoldConfig) (*Result, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, ErrInvalidTree
	}
	if cfg.newSink == nil {
		cfg.newSink = func() sink { return &byteSink{} }
	}

	acc := newAccountant(limits)
	g := newGroup(ctx)
	gctx := g.ctx
	jobs := make(chan leafJob)

	// Single traversal goroutine performs all structural validation
	// (depth/cycle/leaf-count) and hands leaf work to the bounded pool.
	g.goFn(func() error {
		defer close(jobs)
		return traverse(gctx, limits, root, jobs)
	})

	var (
		mu     sync.Mutex
		staged []stagedLeaf
	)

	// The fixed pool size is itself the live-buffer cap: every worker handles
	// at most one leaf buffer at a time, so concurrency can never exceed it.
	for worker := 0; worker < limits.MaxLiveBuffers; worker++ {
		g.goFn(func() error {
			for job := range jobs {
				if err := decodeLeaf(gctx, acc, cfg.newSink, job, &mu, &staged); err != nil {
					return err
				}
			}
			return nil
		})
	}

	if err := g.wait(); err != nil {
		// Failure path: reclaim everything that was staged and publish nothing.
		releaseAll(acc, staged)
		return nil, err
	}

	sort.Slice(staged, func(i, j int) bool { return staged[i].index < staged[j].index })
	leaves := make([]Leaf, len(staged))
	for i, s := range staged {
		leaves[i] = s.leaf
	}

	total, peakLive, peakBytes := acc.snapshot()
	result := &Result{
		Leaves:            leaves,
		TotalBytes:        total,
		PeakLiveBuffers:   peakLive,
		PeakBufferedBytes: peakBytes,
	}

	if cfg.publish != nil {
		if err := cfg.publish(ctx, result); err != nil {
			releaseAll(acc, staged)
			return nil, err
		}
	}
	// Ownership of the payloads transfers to the caller; retire the bytes
	// from the engine's live staging accounting.
	for _, s := range staged {
		acc.reclaim(len(s.sink.bytes()))
	}
	return result, nil
}

// traverse walks the tree depth-first with an ancestor set for cycle
// detection. Structural limits are enforced here, before any decode starts.
func traverse(ctx context.Context, limits Limits, root *Node, jobs chan<- leafJob) error {
	ancestors := make(map[*Node]bool)
	index := 0

	var walk func(node *Node, depth int, path []string) error
	walk = func(node *Node, depth int, path []string) error {
		if node == nil {
			return ErrInvalidTree
		}
		if node.Decode != nil && len(node.Children) > 0 {
			return ErrInvalidTree
		}
		if ancestors[node] {
			// Cyclic reference: reject immediately, do not descend.
			return ErrCycle
		}
		if depth > limits.MaxDepth {
			return ErrDepthExceeded
		}

		ancestors[node] = true
		defer delete(ancestors, node)

		here := append(append(make([]string, 0, len(path)+1), path...), node.Name)

		if node.isLeaf() {
			// Leaf references are counted before any work is scheduled, so a
			// shared leaf consumes one slot per reference.
			if index >= limits.MaxLeaves {
				return ErrLeafLimitExceeded
			}
			index++
			select {
			case jobs <- leafJob{node: node, path: here, index: index - 1}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		for _, child := range node.Children {
			if err := walk(child, depth+1, here); err != nil {
				return err
			}
		}
		return nil
	}

	return walk(root, 0, nil)
}

// decodeLeaf decodes a single leaf reference into a controlled writer and
// stages the result only on full success.
func decodeLeaf(
	ctx context.Context,
	acc *accountant,
	newSink func() sink,
	job leafJob,
	mu *sync.Mutex,
	staged *[]stagedLeaf,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	acc.enterLive()
	defer acc.exitLive()

	dst := newSink()
	writer := &budgetWriter{ctx: ctx, acc: acc, dst: dst}

	decodeErr := job.node.Decode(ctx, writer)
	if decodeErr == nil {
		// A callback can return nil while a prior Write already failed.
		decodeErr = writer.failure()
	}
	if decodeErr != nil {
		dst.release()
		return decodeErr
	}

	mu.Lock()
	*staged = append(*staged, stagedLeaf{
		index: job.index,
		sink:  dst,
		leaf: Leaf{
			Name: job.node.Name,
			Path: append([]string(nil), job.path...),
			Data: dst.bytes(),
		},
	})
	mu.Unlock()
	return nil
}

func releaseAll(acc *accountant, staged []stagedLeaf) {
	for i := range staged {
		acc.reclaim(len(staged[i].sink.bytes()))
		staged[i].sink.release()
	}
}
