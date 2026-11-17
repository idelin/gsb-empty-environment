package netsec

import (
	"errors"
	"fmt"
	"time"
)

// maxEvalDepth bounds recursive evaluation of attacker-supplied ASTs.
const maxEvalDepth = 4096

// OutputValue is one committed public output. Label is retained in the result
// so callers can assert the public-output invariant directly; the executor
// never returns a non-empty label here.
type OutputValue struct {
	Value int
	Label Label
}

// Result is the committed effect of a successful execution. On any error the
// executor returns the zero Result, so no prefix of the outputs is observable.
type Result struct {
	Outputs []OutputValue
}

// Executor evaluates immutable expressions against requests. It is safe for
// concurrent use. Executions of the same Request are serialized by the
// Request; executions of different Requests proceed independently and meet
// only in the registrar's short atomic commit.
type Executor struct {
	registrar *Registrar
	audit     *AuditLog
}

// NewExecutor creates an executor. A nil audit log causes a fresh internal log
// to be used; it is available through Audit.
func NewExecutor(registrar *Registrar, audit *AuditLog) *Executor {
	if audit == nil {
		audit = NewAuditLog()
	}
	return &Executor{registrar: registrar, audit: audit}
}

// Audit returns the executor's audit log.
func (e *Executor) Audit() *AuditLog {
	if e == nil {
		return nil
	}
	return e.audit
}

type labelledValue struct {
	value int
	label Label
}

type transaction struct {
	executor *Executor
	request  *Request

	used       map[*authorization]struct{}
	usedOrder  []*authorization
	outputs    []int
	committed  []Event
	rejections []Event
	depth      int
}

// Execute evaluates expr under request atomically. Authorization consumption
// and output publication are committed together only after the whole
// expression succeeds. Any failure discards all outputs and all pending
// authorization consumption.
func (e *Executor) Execute(request *Request, expr Expr) (Result, error) {
	if e == nil || e.registrar == nil {
		return Result{}, fmt.Errorf("%w: executor has no registrar", ErrInvalidRequest)
	}
	if request == nil {
		return Result{}, ErrInvalidRequest
	}

	// Serializing one request gives each request-level transaction a stable
	// view of its authorizations. Different requests remain concurrent.
	request.execMu.Lock()
	defer request.execMu.Unlock()

	tx := &transaction{
		executor: e,
		request:  request,
		used:     make(map[*authorization]struct{}),
	}
	if _, err := tx.eval(expr, nil, 0); err != nil {
		tx.recordFailure(err)
		return Result{}, err
	}

	if err := e.registrar.commit(request, tx.usedOrder); err != nil {
		tx.recordFailure(err)
		return Result{}, err
	}

	outputs := make([]OutputValue, len(tx.outputs))
	// Pending output values are recorded as zero labels in committed events;
	// build the result from the same transaction.
	for i, value := range tx.outputs {
		outputs[i] = OutputValue{Value: value, Label: 0}
	}
	e.audit.appendAll(tx.committed)
	return Result{Outputs: outputs}, nil
}

func (tx *transaction) eval(expr Expr, env map[string]labelledValue, pc Label) (labelledValue, error) {
	if expr == nil {
		return labelledValue{}, fmt.Errorf("%w: nil child", ErrInvalidExpression)
	}
	if tx.depth >= maxEvalDepth {
		return labelledValue{}, ErrEvaluationDepth
	}
	tx.depth++
	defer func() { tx.depth-- }()

	switch node := expr.(type) {
	case constExpr:
		// A constant computed under a secret branch still depends on that
		// branch's control decision.
		return labelledValue{value: node.value, label: pc}, nil

	case sourceExpr:
		if !node.source.Valid() {
			return labelledValue{}, fmt.Errorf("%w: %d outside [0,%d)", ErrInvalidSource, node.source, MaxSources)
		}
		value, ok := tx.request.input(node.source)
		if !ok {
			return labelledValue{}, fmt.Errorf("%w: s%d", ErrMissingSource, node.source)
		}
		return labelledValue{value: value, label: pc.With(node.source)}, nil

	case addExpr:
		left, err := tx.eval(node.left, env, pc)
		if err != nil {
			return labelledValue{}, err
		}
		right, err := tx.eval(node.right, env, pc)
		if err != nil {
			return labelledValue{}, err
		}
		return labelledValue{
			value: left.value + right.value,
			label: left.label.Union(right.label).Union(pc),
		}, nil

	case conditionalExpr:
		condition, err := tx.eval(node.condition, env, pc)
		if err != nil {
			return labelledValue{}, err
		}
		// The condition is a control dependency for everything evaluated in
		// either branch. It accumulates with any enclosing control label.
		branchPC := pc.Union(condition.label)
		if condition.value != 0 {
			return tx.eval(node.then, env, branchPC)
		}
		return tx.eval(node.otherwise, env, branchPC)

	case letExpr:
		value, err := tx.eval(node.value, env, pc)
		if err != nil {
			return labelledValue{}, err
		}
		inner := cloneEnv(env)
		inner[node.name] = value
		return tx.eval(node.body, inner, pc)

	case varExpr:
		value, ok := env[node.name]
		if !ok {
			return labelledValue{}, fmt.Errorf("%w: %q", ErrUnknownVariable, node.name)
		}
		// Reading an outer binding inside a secret branch adds the current
		// control label as well as the binding's original label.
		return labelledValue{value: value.value, label: value.label.Union(pc)}, nil

	case outputExpr:
		value, err := tx.eval(node.value, env, pc)
		if err != nil {
			return labelledValue{}, err
		}
		if !value.label.IsEmpty() {
			tx.rejections = append(tx.rejections, Event{
				Time:        time.Now(),
				RequestID:   tx.request.id,
				Kind:        EventOutputDenied,
				Label:       value.label,
				Reason:      ErrSecretOutput.Error(),
				OutputIndex: len(tx.outputs),
			})
			return labelledValue{}, fmt.Errorf("%w: label %s", ErrSecretOutput, value.label)
		}
		index := len(tx.outputs)
		tx.outputs = append(tx.outputs, value.value)
		tx.committed = append(tx.committed, Event{
			Time:        time.Now(),
			RequestID:   tx.request.id,
			Kind:        EventOutputPublished,
			Label:       0,
			OutputIndex: index,
			Value:       value.value,
		})
		return labelledValue{value: value.value, label: pc}, nil

	case *DeclassifyNode:
		return tx.evalDeclassify(node, env, pc)

	default:
		return labelledValue{}, fmt.Errorf("%w: %T", ErrInvalidExpression, expr)
	}
}

func (tx *transaction) evalDeclassify(node *DeclassifyNode, env map[string]labelledValue, pc Label) (labelledValue, error) {
	if node == nil || node.core == nil || node.core.expr == nil {
		return labelledValue{}, fmt.Errorf("%w: malformed declassification node", ErrInvalidExpression)
	}
	if node.auth == nil {
		tx.rejectAuthorization(node, 0, ErrMissingAuthorization)
		return labelledValue{}, ErrMissingAuthorization
	}

	auth, allowed, err := tx.executor.registrar.prepare(node.auth, tx.request, node)
	if err != nil {
		tx.rejectAuthorization(node, allowed, err)
		return labelledValue{}, err
	}
	if _, duplicate := tx.used[auth]; duplicate {
		err := fmt.Errorf("%w: same authorization used twice in one execution", ErrAuthorizationReuse)
		tx.rejectAuthorization(node, allowed, err)
		return labelledValue{}, err
	}
	tx.used[auth] = struct{}{}
	tx.usedOrder = append(tx.usedOrder, auth)

	value, err := tx.eval(node.core.expr, env, pc)
	if err != nil {
		return labelledValue{}, err
	}

	// Removal applies to explicit and ambient labels, but the current
	// program-counter label is re-applied afterwards. A declassification
	// inside a secret branch therefore cannot erase the enclosing control
	// dependency, even when the authorization includes that source.
	remaining := value.label.Remove(allowed).Union(pc)
	tx.committed = append(tx.committed, Event{
		Time:      time.Now(),
		RequestID: tx.request.id,
		Kind:      EventDeclassification,
		NodeID:    node.core.id,
		Label:     value.label,
		Allowed:   allowed,
		Remaining: remaining,
	})
	return labelledValue{value: value.value, label: remaining}, nil
}

func (tx *transaction) rejectAuthorization(node *DeclassifyNode, allowed Label, err error) {
	kind := EventAuthorizationDenied
	if err != nil && errors.Is(err, ErrAuthorizationReuse) {
		kind = EventAuthorizationReuse
	}
	event := Event{
		Time:      time.Now(),
		RequestID: tx.request.id,
		Kind:      kind,
		Allowed:   allowed,
		Reason:    "authorization rejected",
	}
	if err != nil {
		event.Reason = err.Error()
	}
	if node != nil {
		event.NodeID = node.NodeID()
	}
	tx.rejections = append(tx.rejections, event)
}

// recordFailure converts pending successful declassification events to
// rolled-back events (never output events), then appends the concrete
// rejection and one final failure event.
func (tx *transaction) recordFailure(err error) {
	events := make([]Event, 0, len(tx.committed)+len(tx.rejections)+1)
	for _, event := range tx.committed {
		if event.Kind != EventDeclassification {
			continue
		}
		event.Kind = EventDeclassificationRolledBack
		event.Reason = "transaction rolled back"
		events = append(events, event)
	}
	events = append(events, tx.rejections...)
	events = append(events, Event{
		Time:      time.Now(),
		RequestID: tx.request.id,
		Kind:      EventExecutionFailed,
		Reason:    err.Error(),
	})
	tx.executor.audit.appendAll(events)
}

func cloneEnv(env map[string]labelledValue) map[string]labelledValue {
	clone := make(map[string]labelledValue, len(env)+1)
	for name, value := range env {
		clone[name] = value
	}
	return clone
}
