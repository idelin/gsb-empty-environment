package netsec

import (
	"fmt"
)

const defaultMaxDefinitionDepth = 64

// FrozenCall is the only data a host execution callback receives for a
// resolved body object. Names and schemas are intentionally absent: the host
// must execute the exact frozen object ID under the definer's privilege.
type FrozenCall struct {
	ObjectID  ObjectID
	Version   int64
	Kind      ObjectKind
	Principal PrincipalID
	Args      []any
}

// CallHandler executes one frozen catalog object. It must not re-resolve a
// name from the call arguments.
type CallHandler func(FrozenCall) (any, error)

// ParameterContext is passed to a caller-side argument evaluator. It is
// deliberately evaluated before any CallHandler invocation.
type ParameterContext struct {
	FunctionID FunctionID
	CallerID   PrincipalID
	DefinerID  PrincipalID
}

// ParameterEvaluator evaluates actual call arguments in the caller's
// security context. It runs before definer privilege is used.
type ParameterEvaluator func(ParameterContext) ([]any, error)

// Invocation supplies the caller identity, pre-evaluated argument values (or
// an evaluator), and any trusted dynamic-SQL ASTs.
type Invocation struct {
	CallerID           PrincipalID
	Args               []any
	EvaluateParameters ParameterEvaluator
	TrustedDynamicAST  map[string]AST
}

// CallTrace records that a frozen object ID was executed. It contains no
// mutable name that could be re-resolved by an audit sink.
type CallTrace struct {
	ObjectID  ObjectID
	Version   int64
	Kind      ObjectKind
	Principal PrincipalID
	Args      []any
	Dynamic   bool
	Value     any
}

// ExecutionResult contains the final body value, all sequence values, and a
// trace of frozen object IDs.
type ExecutionResult struct {
	Value  any
	Values []any
	Trace  []CallTrace
}

// Engine hosts publication, revocation, validation, and execution. A nil
// Catalog makes every invocation fail closed.
type Engine struct {
	Catalog *Catalog
	Execute CallHandler

	functionsByID     map[FunctionID]*PublishedFunction
	functionsByObject map[ObjectID]*PublishedFunction
	revoked           map[FunctionID]bool
	maxDepth          int
}

// NewEngine creates an execution engine.
func NewEngine(c *Catalog, handler CallHandler) *Engine {
	return &Engine{
		Catalog:           c,
		Execute:           handler,
		functionsByID:     make(map[FunctionID]*PublishedFunction),
		functionsByObject: make(map[ObjectID]*PublishedFunction),
		revoked:           make(map[FunctionID]bool),
		maxDepth:          defaultMaxDefinitionDepth,
	}
}

// Publish publishes and registers a function.
func (e *Engine) Publish(def FunctionDefinition) (*PublishedFunction, error) {
	if e == nil || e.Catalog == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrInvalidDefinition)
	}
	pub, err := e.Catalog.Publish(def)
	if err != nil {
		return nil, err
	}
	if err := e.Register(pub); err != nil {
		return nil, err
	}
	return pub, nil
}

// Register makes a published function eligible for nested SECURITY DEFINER
// dispatch. A function object is registered by its frozen object ID when one
// was supplied; otherwise only direct invocation by FunctionID is needed.
func (e *Engine) Register(pub *PublishedFunction) error {
	if e == nil || pub == nil {
		return fmt.Errorf("%w: nil function", ErrInvalidDefinition)
	}
	if e.functionsByID == nil {
		e.functionsByID = make(map[FunctionID]*PublishedFunction)
	}
	if e.functionsByObject == nil {
		e.functionsByObject = make(map[ObjectID]*PublishedFunction)
	}
	if e.revoked == nil {
		e.revoked = make(map[FunctionID]bool)
	}
	if e.maxDepth == 0 {
		e.maxDepth = defaultMaxDefinitionDepth
	}

	if pub.ID == "" {
		return fmt.Errorf("%w: empty function ID", ErrInvalidDefinition)
	}
	if e.functionsByID[pub.ID] != nil {
		return fmt.Errorf("%w: duplicate function ID %q", ErrInvalidDefinition, pub.ID)
	}
	if pub.functionObj == "" {
		// Convenience: if the catalog happens to contain a function object
		// whose ID equals the definition ID, track it for revocation checks.
		if obj, ok := e.Catalog.Object(ObjectID(pub.ID)); ok && obj.Kind == KindFunction {
			pub.functionObj = obj.ID
			pub.functionVer = obj.Version
			pub.functionRef = FrozenRef{
				Kind: obj.Kind, ObjectID: obj.ID, Version: obj.Version,
				SchemaID: obj.SchemaID, Name: obj.Name, Signature: obj.SignatureKey(),
			}
		}
	}
	if pub.functionObj != "" {
		if other := e.functionsByObject[pub.functionObj]; other != nil && other != pub {
			return fmt.Errorf("%w: duplicate function object %q", ErrInvalidDefinition, pub.functionObj)
		}
		e.functionsByObject[pub.functionObj] = pub
	}
	e.functionsByID[pub.ID] = pub
	return nil
}

// Revoke prevents future invocations of a published function.
func (e *Engine) Revoke(id FunctionID) {
	if e == nil {
		return
	}
	if e.revoked == nil {
		e.revoked = make(map[FunctionID]bool)
	}
	e.revoked[id] = true
}

// RevokeFunction is an explicit alias for Revoke.
func (e *Engine) RevokeFunction(id FunctionID) { e.Revoke(id) }

// Revoked reports whether a function has been revoked by this engine.
func (e *Engine) Revoked(id FunctionID) bool {
	if e == nil {
		return true
	}
	return e.revoked[id]
}

// Invoke validates the function and every frozen dependency, then executes
// the body. Caller arguments are evaluated first; only then are frozen object
// callbacks dispatched with definer privilege.
func (e *Engine) Invoke(pub *PublishedFunction, inv Invocation) (ExecutionResult, error) {
	if e == nil || e.Catalog == nil {
		return ExecutionResult{}, fmt.Errorf("%w: nil catalog", ErrInvalidDefinition)
	}
	if pub == nil {
		return ExecutionResult{}, fmt.Errorf("%w: nil published function", ErrInvalidDefinition)
	}
	return e.invoke(pub, inv, 0, nil)
}

func (e *Engine) invoke(pub *PublishedFunction, inv Invocation, depth int, stack map[FunctionID]bool) (ExecutionResult, error) {
	if depth > e.maxDepth {
		return ExecutionResult{}, fmt.Errorf("%w: depth limit %d", ErrRecursiveFunction, e.maxDepth)
	}
	if pub.revoked || pub.Definition.Revoked || e.revoked[pub.ID] {
		return ExecutionResult{}, fmt.Errorf("%w: %q", ErrFunctionRevoked, pub.ID)
	}
	if !pub.allowsCaller(inv.CallerID) {
		return ExecutionResult{}, fmt.Errorf("%w: caller %q", ErrCallerDenied, inv.CallerID)
	}

	// Argument expressions belong to the caller. Evaluate or collect them
	// before any object is called with definer privilege.
	args := inv.Args
	if inv.EvaluateParameters != nil {
		evaluated, err := inv.EvaluateParameters(ParameterContext{
			FunctionID: pub.ID,
			CallerID:   inv.CallerID,
			DefinerID:  pub.definer,
		})
		if err != nil {
			return ExecutionResult{}, err
		}
		args = evaluated
	}

	// A parameter evaluator is caller code and may have touched the catalog.
	// Therefore security and version checks happen after it returns.
	if err := e.validatePublished(pub); err != nil {
		return ExecutionResult{}, err
	}

	if stack == nil {
		stack = make(map[FunctionID]bool)
	}
	if stack[pub.ID] {
		return ExecutionResult{}, fmt.Errorf("%w: %q", ErrRecursiveFunction, pub.ID)
	}
	stack[pub.ID] = true
	defer delete(stack, pub.ID)

	frame := &execFrame{
		engine:  e,
		pub:     pub,
		args:    args,
		frozen:  pub.frozen,
		trusted: inv.TrustedDynamicAST,
		caller:  inv.CallerID,
		depth:   depth,
		stack:   stack,
		result:  ExecutionResult{},
	}
	value, err := frame.executeNodes(pub.body, "body")
	if err != nil {
		return ExecutionResult{}, err
	}
	frame.result.Value = value
	// Values is populated by executeNodes for sequences.
	if frame.result.Values == nil {
		frame.result.Values = []any{}
	}
	return frame.result, nil
}

func (e *Engine) validatePublished(pub *PublishedFunction) error {
	if pub.functionObj != "" {
		if err := e.Catalog.validateFrozen(pub.functionRef); err != nil {
			return fmt.Errorf("%w: function object: %w", ErrFunctionRevoked, err)
		}
	}
	if err := e.Catalog.validateExecutionSecurity(pub.searchPath, pub.definer); err != nil {
		return err
	}
	for _, dep := range pub.deps {
		if err := e.Catalog.validateFrozen(dep.Ref); err != nil {
			return err
		}
		if dep.Ref.SearchIndex < 0 || dep.Ref.SearchIndex >= len(pub.searchPath) {
			return fmt.Errorf("%w: search index for %q", ErrStaleObject, dep.Ref.ObjectID)
		}
		if pub.searchPath[dep.Ref.SearchIndex] != dep.Ref.SchemaID {
			return fmt.Errorf("%w: schema moved for %q", ErrStaleObject, dep.Ref.ObjectID)
		}
	}
	return nil
}

type execFrame struct {
	engine  *Engine
	pub     *PublishedFunction
	args    []any
	frozen  map[string]FrozenRef
	trusted map[string]AST
	caller  PrincipalID
	depth   int
	stack   map[FunctionID]bool
	dynamic bool
	result  ExecutionResult
}

func (f *execFrame) executeNodes(nodes AST, prefix string) (any, error) {
	var last any
	for i, node := range nodes {
		path := prefix + "[" + itoa(i) + "]"
		value, err := f.executeNode(node, path)
		if err != nil {
			return nil, err
		}
		last = value
		f.result.Values = append(f.result.Values, value)
	}
	return last, nil
}

func (f *execFrame) executeNode(node Node, path string) (any, error) {
	if node == nil {
		return nil, fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
	}
	switch n := node.(type) {
	case Ref:
		return f.executeRef(n, path)
	case *Ref:
		if n == nil {
			return nil, fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
		}
		return f.executeRef(*n, path)
	case Seq:
		return f.executeNodes(n.Nodes, path)
	case *Seq:
		if n == nil {
			return nil, fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
		}
		return f.executeNodes(n.Nodes, path)
	case Const:
		return n.Value, nil
	case *Const:
		if n == nil {
			return nil, fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
		}
		return n.Value, nil
	case Param:
		if n.Index < 0 || n.Index >= len(f.args) {
			return nil, fmt.Errorf("%w: parameter index %d at %s", ErrInvalidDefinition, n.Index, path)
		}
		return f.args[n.Index], nil
	case *Param:
		if n == nil {
			return nil, fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
		}
		if n.Index < 0 || n.Index >= len(f.args) {
			return nil, fmt.Errorf("%w: parameter index %d at %s", ErrInvalidDefinition, n.Index, path)
		}
		return f.args[n.Index], nil
	case DynamicSQL:
		return f.executeDynamic(n, path)
	case *DynamicSQL:
		if n == nil {
			return nil, fmt.Errorf("%w: nil AST node at %s", ErrInvalidDefinition, path)
		}
		return f.executeDynamic(*n, path)
	default:
		return nil, fmt.Errorf("%w: unsupported AST node %T at %s", ErrInvalidDefinition, node, path)
	}
}

func (f *execFrame) executeRef(ref Ref, path string) (any, error) {
	argValues, err := f.executeArgs(ref.Args, path+".args")
	if err != nil {
		return nil, err
	}
	frozen, ok := f.frozen[path]
	if !ok {
		return nil, fmt.Errorf("%w: no frozen object at %s", ErrUnresolvedReference, path)
	}
	return f.callFrozen(frozen, argValues, f.dynamic)
}

func (f *execFrame) executeArgs(args AST, prefix string) ([]any, error) {
	out := make([]any, 0, len(args))
	for i, node := range args {
		path := prefix + "[" + itoa(i) + "]"
		value, err := f.executeNode(node, path)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func (f *execFrame) executeDynamic(node DynamicSQL, path string) (any, error) {
	key := node.Key
	if key == "" {
		key = node.SQL
	}
	trusted, ok := f.trusted[key]
	if !ok {
		return nil, fmt.Errorf("%w: key %q at %s", ErrDynamicSQLDenied, key, path)
	}

	// Trusted means "the host vouches for this AST", not "the AST's names are
	// safe". Resolve every reference again against the frozen search path.
	tmp := &PublishedFunction{
		SearchPath: f.pub.searchPath,
		frozen:     make(map[string]FrozenRef),
		dynamic:    make(map[string]DynamicSite),
	}
	dynamicPath := "dynamic:" + key
	if err := f.engine.Catalog.freezeAST(tmp, trusted, dynamicPath); err != nil {
		return nil, err
	}
	if len(tmp.dynamic) != 0 {
		return nil, fmt.Errorf("%w: nested dynamic SQL at %s", ErrDynamicSQLDenied, path)
	}

	oldFrozen := f.frozen
	f.frozen = mergeFrozen(oldFrozen, tmp.frozen)
	oldDynamic := f.dynamic
	f.dynamic = true
	defer func() {
		f.frozen = oldFrozen
		f.dynamic = oldDynamic
	}()

	return f.executeNodes(trusted, dynamicPath)
}

func (f *execFrame) callFrozen(ref FrozenRef, args []any, dynamic bool) (any, error) {
	// Re-check immediately before dispatch as well: earlier body callbacks
	// are host code and can mutate catalog metadata.
	if err := f.engine.Catalog.validateFrozen(ref); err != nil {
		return nil, err
	}
	if f.engine.Catalog.validateExecutionSecurity(f.pub.searchPath, f.pub.definer) != nil {
		return nil, fmt.Errorf("%w: policy changed during execution", ErrUnsafeSchemaAtExecution)
	}

	if nested := f.engine.functionsByObject[ref.ObjectID]; nested != nil && ref.Kind == KindFunction {
		if nested.ID == f.pub.ID {
			return nil, fmt.Errorf("%w: %q", ErrRecursiveFunction, nested.ID)
		}
		result, err := f.engine.invoke(nested, Invocation{
			CallerID: f.caller,
			Args:     args,
		}, f.depth+1, f.stack)
		if err != nil {
			return nil, err
		}
		// Nested traces are appended by the nested frame.
		f.result.Trace = append(f.result.Trace, result.Trace...)
		return result.Value, nil
	}

	if f.engine.Execute == nil {
		return nil, fmt.Errorf("%w: no execution callback for object %q", ErrInvalidDefinition, ref.ObjectID)
	}
	value, err := f.engine.Execute(FrozenCall{
		ObjectID:  ref.ObjectID,
		Version:   ref.Version,
		Kind:      ref.Kind,
		Principal: f.pub.definer,
		Args:      append([]any(nil), args...),
	})
	if err != nil {
		return nil, err
	}
	f.result.Trace = append(f.result.Trace, CallTrace{
		ObjectID:  ref.ObjectID,
		Version:   ref.Version,
		Kind:      ref.Kind,
		Principal: f.pub.definer,
		Args:      append([]any(nil), args...),
		Dynamic:   dynamic,
		Value:     value,
	})
	return value, nil
}

func mergeFrozen(a, b map[string]FrozenRef) map[string]FrozenRef {
	out := make(map[string]FrozenRef, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func itoa(i int) string {
	// Avoid importing strconv in the hot execution path while keeping the
	// same path grammar used by publication.
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
