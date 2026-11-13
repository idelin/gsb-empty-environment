package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Event is one actual action attempt emitted by a script callback.
type Event struct {
	Sequence int      `json:"sequence"`
	Package  Digest   `json:"package"`
	Script   string   `json:"script"`
	Process  []string `json:"process,omitempty"`
	Action   Action   `json:"action"`
	Allowed  bool     `json:"allowed"`
	Code     Code     `json:"code,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// Runner is a synthetic script callback. It receives no host process, file
// descriptor, ambient environment, or credential handle.
type Runner func(context.Context, *Execution) error

// ScriptRef identifies one package script.
type ScriptRef struct {
	Package Digest
	Name    string
}

// Runners maps package scripts to synthetic callbacks.
type Runners map[ScriptRef]Runner

type packageState struct {
	mu         sync.Mutex
	sequence   int
	events     []Event
	violations []Violation
	failed     bool
	provenance map[string]WriteProvenance
}

func newPackageState() *packageState {
	return &packageState{provenance: make(map[string]WriteProvenance)}
}

func (s *packageState) recordAllowed(pkg Digest, script string, process []string, action Action, code Code, detail string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	event := Event{
		Sequence: s.sequence,
		Package:  pkg,
		Script:   script,
		Process:  append([]string(nil), process...),
		Action:   action,
		Allowed:  true,
		Code:     code,
		Detail:   detail,
	}
	s.events = append(s.events, event)
	return event.Sequence
}

func (s *packageState) fail(v Violation) *ViolationError {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	v.Process = append([]string(nil), v.Process...)
	s.events = append(s.events, Event{
		Sequence: s.sequence,
		Package:  v.Package,
		Script:   v.Script,
		Process:  append([]string(nil), v.Process...),
		Action:   v.Action,
		Allowed:  false,
		Code:     v.Code,
		Detail:   v.Message,
	})
	s.violations = append(s.violations, v)
	s.failed = true
	return &ViolationError{Violation: v}
}

func (s *packageState) callbackFailure(v Violation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.Process = append([]string(nil), v.Process...)
	s.violations = append(s.violations, v)
	s.failed = true
}

func (s *packageState) setProvenance(path string, provenance WriteProvenance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provenance.Process = append([]string(nil), provenance.Process...)
	s.provenance[path] = provenance
}

func (s *packageState) hasFailed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

func (s *packageState) snapshot() ([]Event, []Violation, map[string]WriteProvenance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]Event, len(s.events))
	for i, event := range s.events {
		event.Process = append([]string(nil), event.Process...)
		events[i] = event
	}
	violations := make([]Violation, len(s.violations))
	for i, violation := range s.violations {
		violation.Process = append([]string(nil), violation.Process...)
		violations[i] = violation
	}
	return events, violations, copyProvenance(s.provenance), s.failed
}

// Execution is the only capability surface visible to a synthetic script. It
// cannot be constructed with custom roots, grants, or a registry.
type Execution struct {
	roots     *VirtualFS
	state     *packageState
	catalog   catalogSnapshot
	ctx       context.Context
	pkg       Digest
	script    string
	process   []string
	declared  ActionSet
	effective ActionSet
}

// Package returns the executing package digest.
func (e *Execution) Package() Digest { return e.pkg }

// Script returns the root script name.
func (e *Execution) Script() string { return e.script }

// Process returns the callback lineage, with the root script first.
func (e *Execution) Process() []string { return append([]string(nil), e.process...) }

// Context returns the callback context.
func (e *Execution) Context() context.Context {
	if e.ctx == nil {
		return context.Background()
	}
	return e.ctx
}

// Declared returns the actions declared for this process.
func (e *Execution) Declared() ActionSet { return e.declared }

// Granted returns the effective actions after policy, registry, and inherited
// intersections.
func (e *Execution) Granted() ActionSet { return e.effective }

// Read performs a source-root read after declaration and authorization checks.
func (e *Execution) Read(path string) ([]byte, error) {
	action := Read(path)
	if err := e.begin(action); err != nil {
		return nil, err
	}
	data, err := e.roots.readSource(e.pkg, path)
	if err != nil {
		return nil, e.handlePathResult(action, err)
	}
	e.state.recordAllowed(e.pkg, e.script, e.process, action, "", "")
	return data, nil
}

// Write performs an output-root write after declaration and authorization
// checks. The write is staged and remains invisible until publication.
func (e *Execution) Write(path string, data []byte) error {
	action := Write(path)
	if err := e.begin(action); err != nil {
		return err
	}
	if err := e.roots.writeOutput(e.pkg, path, data); err != nil {
		return e.handlePathResult(action, err)
	}
	sequence := e.state.recordAllowed(e.pkg, e.script, e.process, action, "", "")
	e.state.setProvenance(path, WriteProvenance{
		Script:  e.script,
		Process: append([]string(nil), e.process...),
		Action:  action,
		Event:   sequence,
	})
	return nil
}

// Exec validates an exact host-registered tool ID. It never starts a process.
func (e *Execution) Exec(toolID string) error {
	action := Exec(toolID)
	if err := e.beginDeclared(action); err != nil {
		return err
	}
	if _, ok := e.catalog.tools[toolID]; !ok {
		return e.deny(CodeUnregisteredTool, action, "tool ID is not registered by the host")
	}
	if !e.effective.Has(action) {
		return e.deny(CodePolicyDenied, action, "host policy did not grant the declared action")
	}
	e.state.recordAllowed(e.pkg, e.script, e.process, action, "", "")
	return nil
}

// Connect validates an exact host-registered endpoint ID. It never opens a
// network connection.
func (e *Execution) Connect(endpointID string) error {
	action := Connect(endpointID)
	if err := e.beginDeclared(action); err != nil {
		return err
	}
	if _, ok := e.catalog.endpoints[endpointID]; !ok {
		return e.deny(CodeUnknownEndpoint, action, "endpoint ID is not registered by the host")
	}
	if !e.effective.Has(action) {
		return e.deny(CodePolicyDenied, action, "host policy did not grant the declared action")
	}
	e.state.recordAllowed(e.pkg, e.script, e.process, action, "", "")
	return nil
}

// Env returns a value only for an exact, declared, policy-authorized name. The
// value is never placed in an event, plan, violation, or error.
func (e *Execution) Env(name string) (string, error) {
	action := Env(name)
	if err := e.beginDeclared(action); err != nil {
		return "", err
	}
	value, ok := e.catalog.env[name]
	if !ok {
		return "", e.deny(CodeEnvironmentAbsent, action, "environment name is not configured by the host")
	}
	if !e.effective.Has(action) {
		return "", e.deny(CodePolicyDenied, action, "host policy did not grant the declared action")
	}
	e.state.recordAllowed(e.pkg, e.script, e.process, action, "", "")
	return value, nil
}

// Spawn runs a synthetic child callback with effective grants equal to
// parent-effective ∩ child-declared. The child cannot add an action that the
// parent did not already hold.
func (e *Execution) Spawn(ctx context.Context, name string, declared []Action, runner Runner) error {
	if e.state.hasFailed() {
		return &ViolationError{Violation: Violation{
			Code:    CodeExecutionAborted,
			Package: e.pkg,
			Script:  e.script,
			Process: append(append([]string(nil), e.process...), name),
			Message: "parent execution has already failed",
		}}
	}
	if err := validateScriptName(name); err != nil {
		return e.failWithoutEvent(CodeInvalidAction, append(append([]string(nil), e.process...), name), Action{}, "invalid child process name")
	}
	childDeclared := NewActionSet()
	childProcess := append(append([]string(nil), e.process...), name)
	for _, action := range declared {
		if err := action.Validate(); err != nil {
			code := CodeInvalidAction
			var pathErr *PathError
			if errors.As(err, &pathErr) {
				code = pathErrorCode(pathErr.Code)
			}
			return e.failWithoutEvent(code, childProcess, action, "invalid child declaration")
		}
		if !e.effective.Has(action) {
			return e.failWithoutEvent(CodeSpawnEscalation, childProcess, action, "child declaration exceeds inherited effective capabilities")
		}
		childDeclared = childDeclared.Union(NewActionSet(action))
	}
	childEffective := childDeclared.Intersect(e.effective)
	if runner == nil {
		e.recordCallbackFailureFor(childProcess, "child callback is missing")
		return fmt.Errorf("netsec: child callback %q is missing", name)
	}
	if ctx == nil {
		ctx = e.Context()
	}
	child := &Execution{
		roots:     e.roots,
		state:     e.state,
		catalog:   e.catalog,
		ctx:       ctx,
		pkg:       e.pkg,
		script:    e.script,
		process:   childProcess,
		declared:  childDeclared,
		effective: childEffective,
	}
	err, panicked := invokeRunner(ctx, runner, child)
	switch {
	case panicked:
		e.recordCallbackFailureFor(childProcess, "child callback panicked")
		return fmt.Errorf("netsec: child callback %q panicked", name)
	case err != nil:
		var violation *ViolationError
		if errors.As(err, &violation) {
			return err
		}
		e.recordCallbackFailureFor(childProcess, "child callback returned an error")
		return fmt.Errorf("netsec: child callback %q failed", name)
	case child.state.hasFailed():
		return fmt.Errorf("netsec: child callback %q failed", name)
	default:
		return nil
	}
}

func (e *Execution) begin(action Action) *ViolationError {
	if err := e.beginDeclared(action); err != nil {
		return err
	}
	if !e.effective.Has(action) {
		return e.deny(CodePolicyDenied, action, "host policy did not grant the declared action")
	}
	return nil
}

func (e *Execution) beginDeclared(action Action) *ViolationError {
	if e.state.hasFailed() {
		return e.deny(CodeExecutionAborted, action, "execution is already aborted")
	}
	if err := action.Validate(); err != nil {
		var pathErr *PathError
		if errors.As(err, &pathErr) {
			return e.deny(pathErrorCode(pathErr.Code), action, pathErr.Error())
		}
		return e.deny(CodeInvalidAction, action, err.Error())
	}
	if !e.declared.Has(action) {
		return e.deny(CodeUndeclaredAction, action, "action was not declared by this script")
	}
	return nil
}

func (e *Execution) handlePathResult(action Action, err error) error {
	var pathErr *PathError
	if !errors.As(err, &pathErr) {
		return err
	}
	switch pathErr.Code {
	case PathSymlinkParent, PathSymlinkTarget, PathAbsolute, PathDotSegment:
		return e.deny(pathErrorCode(pathErr.Code), action, pathErr.Error())
	default:
		e.state.recordAllowed(e.pkg, e.script, e.process, action, CodeIOFailure, pathErr.Error())
		return err
	}
}

func (e *Execution) deny(code Code, action Action, message string) *ViolationError {
	return e.denyForProcess(code, e.process, action, message)
}

func (e *Execution) denyForProcess(code Code, process []string, action Action, message string) *ViolationError {
	return e.state.fail(Violation{
		Code:    code,
		Package: e.pkg,
		Script:  e.script,
		Process: append([]string(nil), process...),
		Action:  action,
		Message: message,
	})
}

func (e *Execution) failWithoutEvent(code Code, process []string, action Action, message string) *ViolationError {
	violation := Violation{
		Code:    code,
		Package: e.pkg,
		Script:  e.script,
		Process: append([]string(nil), process...),
		Action:  action,
		Message: message,
	}
	e.state.callbackFailure(violation)
	return &ViolationError{Violation: violation}
}

func (e *Execution) recordCallbackFailureFor(process []string, message string) {
	e.state.callbackFailure(Violation{
		Code:    CodeCallbackFailure,
		Package: e.pkg,
		Script:  e.script,
		Process: append([]string(nil), process...),
		Message: message,
	})
}

func invokeRunner(ctx context.Context, runner Runner, execution *Execution) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
			err = nil
		}
	}()
	return runner(ctx, execution), false
}
