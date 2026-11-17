package netsec

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// Expander performs budgeted expansions. It is immutable after construction
// and safe for concurrent Expand calls. Stats reports the most recently
// completed attempt.
type Expander struct {
	budget       Budget
	concurrency  int
	stageFactory StageFactory

	statsMu sync.Mutex
	stats   Stats
}

// New validates budget and options and returns an Expander.
func New(budget Budget, opts ...Option) (*Expander, error) {
	if err := budget.validate(); err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	if cfg.concurrency < 1 {
		return nil, fmt.Errorf("%w: concurrency must be at least 1", ErrInvalidConfig)
	}
	if cfg.stageFactory == nil {
		return nil, fmt.Errorf("%w: stage factory must not be nil", ErrInvalidConfig)
	}
	return &Expander{
		budget:       budget,
		concurrency:  cfg.concurrency,
		stageFactory: cfg.stageFactory,
	}, nil
}

// Expand expands roots under budget. It returns a non-nil Result only if every
// referenced leaf decodes successfully. On any failure it returns a nil Result
// and resets all staging buffers.
func Expand(ctx context.Context, roots []*Node, budget Budget, opts ...Option) (*Result, error) {
	e, err := New(budget, opts...)
	if err != nil {
		return nil, err
	}
	return e.Expand(ctx, roots)
}

// Stats returns statistics for the most recently completed Expand call.
func (e *Expander) Stats() Stats {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	return e.stats
}

func (e *Expander) setStats(s Stats) {
	e.statsMu.Lock()
	e.stats = s
	e.statsMu.Unlock()
}

type planNode struct {
	name     string
	kind     Kind
	children []*planNode
	task     *leafTask
}

type leafTask struct {
	node  *Node
	label string

	stage  Stage
	writer *controlledWriter
	err    error
}

type runState struct {
	exp  *Expander
	ctx  context.Context
	stop context.CancelCauseFunc

	budget Budget

	mu      sync.Mutex
	decoded int64
	live    int64
	peak    int64
	leaves  int64
	depth   int

	errMu    sync.Mutex
	firstErr error
}

// Expand expands roots and atomically publishes the result.
func (e *Expander) Expand(ctx context.Context, roots []*Node) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, stop := context.WithCancelCause(ctx)
	run := &runState{exp: e, ctx: runCtx, stop: stop, budget: e.budget}
	defer stop(nil)

	var (
		planRoots []*planNode
		tasks     []*leafTask
		result    *Result
	)

	err := func() error {
		if err := contextErr(runCtx); err != nil {
			return err
		}
		if err := validateTree(runCtx, roots); err != nil {
			return err
		}
		var err error
		planRoots, tasks, err = run.buildPlan(roots)
		if err != nil {
			return err
		}
		if err := contextErr(runCtx); err != nil {
			return err
		}

		run.execute(tasks)
		if err := run.failure(); err != nil {
			return err
		}
		if err := contextErr(runCtx); err != nil {
			return err
		}

		result = &Result{
			Roots: buildResult(planRoots),
			Stats: run.stats(),
		}
		return nil
	}()

	stats := run.stats()
	e.setStats(stats)
	if err != nil {
		cleanupTasks(tasks)
		return nil, err
	}
	return result, nil
}

func contextErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return err
	}
	return nil
}

// validateTree rejects malformed nodes and cycles before any decoder runs.
// Shared (DAG) references are allowed. Iterative traversal avoids unbounded
// recursion on hostile inputs.
func validateTree(ctx context.Context, roots []*Node) error {
	const (
		gray  = 1
		black = 2
	)
	color := make(map[*Node]uint8)

	validateNode := func(n *Node) error {
		if n == nil {
			return fmt.Errorf("%w: nil node", ErrInvalidTree)
		}
		if n.Decode != nil && len(n.Children) != 0 {
			return fmt.Errorf("%w: node %q is both a stream and a container", ErrInvalidTree, n.Name)
		}
		return nil
	}

	type frame struct {
		node *Node
		next int
	}

	for _, root := range roots {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if err := validateNode(root); err != nil {
			return err
		}
		switch color[root] {
		case gray:
			return fmt.Errorf("%w: node %q", ErrCycle, root.Name)
		case black:
			continue
		}

		color[root] = gray
		stack := []frame{{node: root}}
		for len(stack) > 0 {
			if err := contextErr(ctx); err != nil {
				return err
			}
			f := &stack[len(stack)-1]
			if f.next < len(f.node.Children) {
				child := f.node.Children[f.next]
				f.next++
				if err := validateNode(child); err != nil {
					return err
				}
				switch color[child] {
				case gray:
					return fmt.Errorf("%w: node %q", ErrCycle, child.Name)
				case black:
					continue
				}
				color[child] = gray
				stack = append(stack, frame{node: child})
				continue
			}
			color[f.node] = black
			stack = stack[:len(stack)-1]
		}
	}
	return nil
}

// buildPlan walks every tree reference, enforcing depth and leaf budgets and
// creating one decode task per terminal reference. It never starts a decoder.
func (r *runState) buildPlan(roots []*Node) ([]*planNode, []*leafTask, error) {
	var (
		planRoots []*planNode
		tasks     []*leafTask
		leaves    int64
		maxDepth  int
	)
	defer func() {
		r.mu.Lock()
		r.leaves = leaves
		r.depth = maxDepth
		r.mu.Unlock()
	}()

	type frame struct {
		node   *Node
		depth  int
		parent *planNode
		pn     *planNode
		next   int
	}

	for _, root := range roots {
		if err := contextErr(r.ctx); err != nil {
			return nil, nil, err
		}
		stack := []frame{{node: root, depth: 1}}
		for len(stack) > 0 {
			if err := contextErr(r.ctx); err != nil {
				return nil, nil, err
			}
			f := &stack[len(stack)-1]
			if f.pn == nil {
				if f.depth > r.budget.MaxDepth {
					return nil, nil, &BudgetError{
						Dimension: DimensionDepth,
						Limit:     int64(r.budget.MaxDepth),
						Attempted: int64(f.depth),
						Node:      f.node.Name,
					}
				}
				pn := &planNode{name: f.node.Name}
				f.pn = pn
				if f.parent == nil {
					planRoots = append(planRoots, pn)
				} else {
					f.parent.children = append(f.parent.children, pn)
				}

				if f.node.Decode != nil {
					pn.kind = KindStream
					if leaves >= r.budget.MaxLeaves {
						return nil, nil, &BudgetError{
							Dimension: DimensionLeaves,
							Limit:     r.budget.MaxLeaves,
							Attempted: saturatingAdd(leaves, 1),
							Node:      f.node.Name,
						}
					}
					leaves++
					if f.depth > maxDepth {
						maxDepth = f.depth
					}
					task := &leafTask{
						node:  f.node,
						label: f.node.Name,
					}
					pn.task = task
					tasks = append(tasks, task)
					stack = stack[:len(stack)-1]
					continue
				}

				pn.kind = KindContainer
				if len(f.node.Children) == 0 {
					if leaves >= r.budget.MaxLeaves {
						return nil, nil, &BudgetError{
							Dimension: DimensionLeaves,
							Limit:     r.budget.MaxLeaves,
							Attempted: saturatingAdd(leaves, 1),
							Node:      f.node.Name,
						}
					}
					leaves++
					if f.depth > maxDepth {
						maxDepth = f.depth
					}
					stack = stack[:len(stack)-1]
					continue
				}
			}

			if f.next < len(f.node.Children) {
				child := f.node.Children[f.next]
				f.next++
				stack = append(stack, frame{node: child, depth: f.depth + 1, parent: f.pn})
				continue
			}
			stack = stack[:len(stack)-1]
		}
	}

	return planRoots, tasks, nil
}

func (r *runState) execute(tasks []*leafTask) {
	if len(tasks) == 0 {
		return
	}
	workers := r.exp.concurrency
	if workers > len(tasks) {
		workers = len(tasks)
	}

	jobs := make(chan *leafTask)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for task := range jobs {
				r.runTask(task)
			}
		}()
	}

dispatch:
	for _, task := range tasks {
		if r.ctx.Err() != nil {
			break
		}
		select {
		case jobs <- task:
		case <-r.ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
}

func (r *runState) runTask(task *leafTask) {
	completed := false
	defer func() {
		if completed {
			return
		}
		if recovered := recover(); recovered != nil {
			task.err = fmt.Errorf("netsec: decoder for %q panicked: %v", task.node.Name, recovered)
		} else {
			task.err = fmt.Errorf("netsec: decoder for %q terminated without returning", task.node.Name)
		}
		if task.writer != nil {
			task.writer.close()
		}
		r.fail(task.err)
	}()

	if err := contextErr(r.ctx); err != nil {
		task.err = err
		completed = true
		return
	}
	stage := r.exp.stageFactory(task.label)
	if stage == nil {
		task.err = fmt.Errorf("%w: stage factory returned nil for %q", ErrInvalidConfig, task.label)
		completed = true
		r.fail(task.err)
		return
	}
	task.stage = stage
	writer := &controlledWriter{run: r, task: task}
	task.writer = writer

	err := task.node.Decode(r.ctx, writer)
	writer.close()
	completed = true
	if err != nil {
		task.err = err
		r.fail(err)
	}
}

func (r *runState) fail(err error) {
	if err == nil {
		return
	}
	r.errMu.Lock()
	if r.firstErr == nil {
		r.firstErr = err
		r.stop(err)
	}
	r.errMu.Unlock()
}

func (r *runState) failure() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.firstErr
}

func (r *runState) reserve(n int64, label string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n > r.budget.MaxDecodedBytes-r.decoded {
		return &BudgetError{
			Dimension: DimensionDecodedBytes,
			Limit:     r.budget.MaxDecodedBytes,
			Attempted: saturatingAdd(r.decoded, n),
			Node:      label,
		}
	}
	if n > r.budget.MaxLiveBuffer-r.live {
		return &BudgetError{
			Dimension: DimensionLiveBuffer,
			Limit:     r.budget.MaxLiveBuffer,
			Attempted: saturatingAdd(r.live, n),
			Node:      label,
		}
	}
	r.decoded += n
	r.live += n
	if r.live > r.peak {
		r.peak = r.live
	}
	return nil
}

func (r *runState) rollback(n int64) {
	if n <= 0 {
		return
	}
	r.mu.Lock()
	r.decoded -= n
	r.live -= n
	r.mu.Unlock()
}

func (r *runState) stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Stats{
		DecodedBytes:  r.decoded,
		Leaves:        r.leaves,
		MaxDepth:      r.depth,
		PeakLiveBytes: r.peak,
	}
}

type controlledWriter struct {
	run  *runState
	task *leafTask

	mu     sync.Mutex
	closed bool
	err    error
}

func (w *controlledWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrWriterClosed
	}
	if w.err != nil {
		return 0, w.err
	}
	if err := contextErr(w.run.ctx); err != nil {
		w.err = err
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}

	if err := w.run.reserve(int64(len(p)), w.task.label); err != nil {
		w.err = err
		w.run.fail(err)
		return 0, err
	}

	n, err := w.task.stage.Write(p)
	if n < 0 {
		n = 0
	}
	if n > len(p) {
		n = len(p)
		if err == nil {
			err = fmt.Errorf("netsec: stage wrote more than requested")
		}
	}
	if n < len(p) {
		w.run.rollback(int64(len(p) - n))
		if err == nil {
			err = io.ErrShortWrite
		}
	}
	if err != nil {
		w.err = err
		w.run.fail(err)
	}
	return n, err
}

func (w *controlledWriter) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

func buildResult(plans []*planNode) []*ResultNode {
	if len(plans) == 0 {
		return nil
	}

	type work struct {
		plan *planNode
		node *ResultNode
	}

	out := make([]*ResultNode, len(plans))
	stack := make([]work, 0, len(plans))
	for i := len(plans) - 1; i >= 0; i-- {
		node := &ResultNode{Name: plans[i].name, Kind: plans[i].kind}
		out[i] = node
		if plans[i].kind == KindStream {
			if plans[i].task != nil && plans[i].task.stage != nil {
				node.Data = plans[i].task.stage.Bytes()
			}
			continue
		}
		stack = append(stack, work{plan: plans[i], node: node})
	}

	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		children := item.plan.children
		if len(children) == 0 {
			continue
		}
		item.node.Children = make([]*ResultNode, len(children))
		for i := len(children) - 1; i >= 0; i-- {
			child := &ResultNode{Name: children[i].name, Kind: children[i].kind}
			item.node.Children[i] = child
			if children[i].kind == KindStream {
				if children[i].task != nil && children[i].task.stage != nil {
					child.Data = children[i].task.stage.Bytes()
				}
				continue
			}
			stack = append(stack, work{plan: children[i], node: child})
		}
	}
	return out
}

func cleanupTasks(tasks []*leafTask) {
	for _, task := range tasks {
		if task == nil || task.stage == nil {
			continue
		}
		resetStage(task.stage)
		task.stage = nil
	}
}

func resetStage(stage Stage) {
	defer func() { _ = recover() }()
	stage.Reset()
}
