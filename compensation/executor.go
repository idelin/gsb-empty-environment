package compensation

import "sync"

// Executor runs a dependency graph of steps and, on failure, produces
// a compensation plan covering every completed step — including the
// failed step's ancestors and any completed parallel branches.
type Executor struct {
	// MaxCompensateAttempts bounds how many times a single failing
	// compensation is attempted before it is demoted to a manual item.
	// Zero means no retries beyond the first attempt.
	MaxCompensateAttempts int

	// PlanPath, when set, makes the executor persist the compensation
	// plan after every state transition so it can be resumed later.
	PlanPath string
}

// Result summarizes a forward execution.
type Result struct {
	// Completed lists step IDs that ran successfully, in run order.
	Completed []string
	// FailedStep is the step whose Run error triggered compensation.
	FailedStep string
	// Plan is the compensation plan. It is nil when every step
	// succeeded and no compensation is needed.
	Plan *Plan
	// Report is the outcome of running the plan. Nil when Plan is nil.
	Report *RollbackReport
}

type stepOutcome struct {
	id  string
	err error
}

// Execute runs the steps level by level. Steps within one level run
// concurrently. If any step fails, remaining levels are not started,
// in-flight steps are awaited, and every completed step is compensated
// in reverse dependency order.
func (e *Executor) Execute(ctx Context, steps []Step) (*Result, error) {
	g, err := buildGraph(steps)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	var failedStep string
	var runErr error

loop:
	for _, level := range g.levels() {
		outcomes := make(chan stepOutcome, len(level))
		var wg sync.WaitGroup
		for _, id := range level {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				outcomes <- stepOutcome{id: id, err: g.steps[id].Run(ctx)}
			}(id)
		}
		wg.Wait()
		close(outcomes)
		for oc := range outcomes {
			if oc.err != nil {
				if runErr == nil {
					runErr = oc.err
					failedStep = oc.id
				}
			} else {
				res.Completed = append(res.Completed, oc.id)
			}
		}
		if runErr != nil {
			break loop
		}
	}
	res.FailedStep = failedStep
	if runErr == nil {
		return res, nil
	}

	plan, err := e.newPlan(g, res.Completed)
	if err != nil {
		return nil, err
	}
	res.Plan = plan
	res.Report = plan.Run(ctx, g.registry(), e.MaxCompensateAttempts)
	return res, nil
}

// newPlan builds a compensation plan for the completed steps and
// persists it immediately when PlanPath is configured.
func (e *Executor) newPlan(g *graph, completed []string) (*Plan, error) {
	done := make(map[string]bool, len(completed))
	for _, id := range completed {
		done[id] = true
	}
	p := &Plan{path: e.PlanPath}
	for _, id := range g.compensationOrder(done) {
		entry := &PlanEntry{StepID: id, Status: StatusPending}
		if g.steps[id].Compensate == nil {
			entry.Status = StatusManual
			entry.LastError = "step has no compensation handler"
		}
		p.Entries = append(p.Entries, entry)
	}
	if e.PlanPath != "" {
		if err := p.Save(e.PlanPath); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// registry maps step IDs to their compensation handlers.
func (g *graph) registry() map[string]func(Context) error {
	reg := make(map[string]func(Context) error, len(g.steps))
	for id, s := range g.steps {
		reg[id] = s.Compensate
	}
	return reg
}

// Resume reloads a persisted plan from path and continues executing
// it with the given compensation handlers. Pending entries run first;
// previously failed entries are retried while attempts remain.
// Manual entries are never retried automatically.
func Resume(ctx Context, path string, registry map[string]func(Context) error, maxAttempts int) (*Plan, *RollbackReport, error) {
	p, err := LoadPlan(path)
	if err != nil {
		return nil, nil, err
	}
	p.path = path
	report := p.Run(ctx, registry, maxAttempts)
	return p, report, nil
}
