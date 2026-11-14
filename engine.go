package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"sync"
)

// Engine compiles operations and performs fail-closed field authorization.
// The zero value is usable: a nil Policy allows all fields after the built-in
// tenant-ID guard.
type Engine struct {
	Policy Policy

	// DisableTenantIsolation turns off the built-in check that rejects an
	// explicitly foreign tenant encoded in an ID-like argument. It is enabled
	// by default, including in the zero value.
	DisableTenantIsolation bool

	MaxDepth         int
	MaxNodes         int
	MaxFragmentDepth int

	persistedMu sync.RWMutex
	persisted   map[string]string
}

// Authorizer is an alias for Engine.
type Authorizer = Engine

// NewEngine creates an authorization engine. A nil policy allows all fields
// only after the built-in cross-tenant node-ID guard.
func NewEngine(policy Policy) *Engine {
	return &Engine{
		Policy:           policy,
		MaxDepth:         defaultMaxDepth,
		MaxNodes:         defaultMaxNodes,
		MaxFragmentDepth: defaultMaxFragmentDepth,
		persisted:        make(map[string]string),
	}
}

// NewAuthorizer is an alias for NewEngine.
func NewAuthorizer(policy Policy) *Authorizer {
	return NewEngine(policy)
}

// Compile normalizes an operation with this engine's resource limits.
func (e *Engine) Compile(query string, operationName ...string) (*StaticPlan, error) {
	name := ""
	if len(operationName) > 1 {
		return nil, fmt.Errorf("%w: at most one operation name may be supplied", ErrInvalidQuery)
	}
	if len(operationName) == 1 {
		name = operationName[0]
	}
	limits := e.limits()
	return compileWithLimits(query, name, limits.maxDepth, limits.maxNodes, limits.maxFragmentDepth)
}

type engineLimits struct {
	maxDepth         int
	maxNodes         int
	maxFragmentDepth int
}

func (e *Engine) limits() engineLimits {
	result := engineLimits{
		maxDepth:         defaultMaxDepth,
		maxNodes:         defaultMaxNodes,
		maxFragmentDepth: defaultMaxFragmentDepth,
	}
	if e == nil {
		return result
	}
	if e.MaxDepth > 0 {
		result.maxDepth = e.MaxDepth
	}
	if e.MaxNodes > 0 {
		result.maxNodes = e.MaxNodes
	}
	if e.MaxFragmentDepth > 0 {
		result.maxFragmentDepth = e.MaxFragmentDepth
	}
	return result
}

// Authorize parses/compiles the operation, resolves variables and defaults,
// evaluates @skip/@include for every occurrence, re-authorizes every active
// alias independently, and only then merges safe response paths.
func (e *Engine) Authorize(ctx context.Context, request Request) (*Plan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	query := request.Query
	if query == "" && request.PersistedQueryID != "" {
		persisted, err := e.lookupPersisted(request.PersistedQueryID)
		if err != nil {
			return nil, err
		}
		query = persisted
	}
	if query == "" {
		return nil, fmt.Errorf("%w: query is empty", ErrInvalidQuery)
	}
	static, err := e.Compile(query, request.OperationName)
	if err != nil {
		return nil, err
	}
	variables, err := resolveVariables(static.Operation, request.Variables)
	if err != nil {
		return nil, err
	}
	return e.evaluate(ctx, static, request.Identity, variables, request.BatchID, request.PersistedQueryID)
}

// AuthorizeStatic evaluates a previously compiled plan. Static plans are
// immutable and can be shared by direct, batched, and persisted executions;
// variables, directives, identities, and policy decisions are always
// re-evaluated for each call.
func (e *Engine) AuthorizeStatic(ctx context.Context, static *StaticPlan, variables map[string]any, identity Identity) (*Plan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if static == nil || static.Operation == nil {
		return nil, fmt.Errorf("%w: static plan is nil", ErrInvalidQuery)
	}
	resolved, err := resolveVariables(static.Operation, variables)
	if err != nil {
		return nil, err
	}
	return e.evaluate(ctx, static, identity, resolved, "", "")
}

// AuthorizeBatch authorizes each operation independently. The batch container
// is irrelevant to each operation's plan digest.
func (e *Engine) AuthorizeBatch(ctx context.Context, requests []Request) (*BatchResult, error) {
	result := &BatchResult{Plans: make([]*Plan, 0, len(requests))}
	var joined []error
	for index := range requests {
		request := requests[index]
		if request.BatchID == "" {
			request.BatchID = "batch-" + strconv.Itoa(index)
		}
		plan, err := e.Authorize(ctx, request)
		result.Plans = append(result.Plans, plan)
		if err != nil {
			result.Errors = append(result.Errors, err)
			joined = append(joined, fmt.Errorf("batch item %d: %w", index, err))
		} else {
			result.Errors = append(result.Errors, nil)
		}
	}
	return result, errors.Join(joined...)
}

// RegisterPersistedQuery stores a persisted query body by its opaque ID.
func (e *Engine) RegisterPersistedQuery(id, query string) error {
	if e == nil {
		return fmt.Errorf("%w: engine is nil", ErrInvalidQuery)
	}
	if id == "" {
		return fmt.Errorf("%w: persisted query ID is empty", ErrInvalidQuery)
	}
	if query == "" {
		return fmt.Errorf("%w: persisted query body is empty", ErrInvalidQuery)
	}
	e.persistedMu.Lock()
	defer e.persistedMu.Unlock()
	if e.persisted == nil {
		e.persisted = make(map[string]string)
	}
	e.persisted[id] = query
	return nil
}

// AuthorizePersisted authorizes a registered persisted operation. Its plan
// digest is identical to authorizing the same query text directly.
func (e *Engine) AuthorizePersisted(ctx context.Context, id string, variables map[string]any, identity Identity, operationName ...string) (*Plan, error) {
	query, err := e.lookupPersisted(id)
	if err != nil {
		return nil, err
	}
	if len(operationName) > 1 {
		return nil, fmt.Errorf("%w: at most one operation name may be supplied", ErrInvalidQuery)
	}
	selectedOperation := ""
	if len(operationName) == 1 {
		selectedOperation = operationName[0]
	}
	return e.Authorize(ctx, Request{
		Query:            query,
		OperationName:    selectedOperation,
		Variables:        variables,
		Identity:         identity,
		PersistedQueryID: id,
	})
}

func (e *Engine) lookupPersisted(id string) (string, error) {
	if e == nil {
		return "", fmt.Errorf("%w: persisted query store is nil", ErrInvalidQuery)
	}
	e.persistedMu.RLock()
	query, exists := e.persisted[id]
	e.persistedMu.RUnlock()
	if !exists {
		return "", fmt.Errorf("%w: persisted query %q is not registered", ErrInvalidQuery, id)
	}
	return query, nil
}

func (e *Engine) evaluate(ctx context.Context, static *StaticPlan, identity Identity, variables map[string]any, batchID, persistedID string) (*Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPolicy, err)
	}
	policy := Policy(AllowAllPolicy{})
	if e != nil && e.Policy != nil {
		policy = e.Policy
	}
	clonedVariables := map[string]any{}
	if cloned, ok := cloneJSONValue(variables).(map[string]any); ok {
		clonedVariables = cloned
	}
	plan := &Plan{
		Static:        static,
		Digest:        static.Digest,
		OperationName: static.OperationName,
		Identity:      identity,
		IdentityKey:   identity.Key(),
		Variables:     clonedVariables,
		Nodes:         make([]*AuthNode, 0, len(static.Nodes)),
	}
	var failures []AuthFailure
	var conflict bool
	var policyFailure bool
	var invalidRuntime bool
	activeByPath := make(map[string][]*AuthNode)
	pathOrder := make([]Path, 0)
	denied := make(map[string]struct{})
	deniedOrder := make([]Path, 0)
	blockedOrder := make([]Path, 0)
	markDenied := func(path Path) {
		key := path.Key()
		if _, exists := denied[key]; exists {
			return
		}
		denied[key] = struct{}{}
		deniedOrder = append(deniedOrder, path.Clone())
	}

	for _, template := range static.Nodes {
		if err := ctx.Err(); err != nil {
			return plan, fmt.Errorf("%w: %v", ErrPolicy, err)
		}
		directives, active, err := evaluateDirectives(template.Conditions, variables)
		if err != nil {
			invalidRuntime = true
			markDenied(template.Path)
			failures = append(failures, AuthFailure{
				Path:      template.Path,
				FieldName: template.FieldName,
				Alias:     template.Alias,
				NodeID:    template.ID,
				Reason:    err.Error(),
			})
			plan.Nodes = append(plan.Nodes, &AuthNode{
				ID:          template.ID,
				ParentID:    template.ParentID,
				TemplateID:  template.ID,
				Order:       template.Order,
				ParentType:  template.ParentType,
				FieldName:   template.FieldName,
				Alias:       template.Alias,
				ResponseKey: template.ResponseKey,
				Path:        template.Path.Clone(),
				Directives:  directives,
				Active:      false,
				IdentityKey: plan.IdentityKey,
				Error:       err.Error(),
				Depth:       template.Depth,
				Position:    template.Position,
			})
			continue
		}
		if !active {
			node := &AuthNode{
				ID:          template.ID,
				ParentID:    template.ParentID,
				TemplateID:  template.ID,
				Order:       template.Order,
				ParentType:  template.ParentType,
				FieldName:   template.FieldName,
				Alias:       template.Alias,
				ResponseKey: template.ResponseKey,
				Path:        template.Path.Clone(),
				Directives:  directives,
				Active:      false,
				IdentityKey: plan.IdentityKey,
				Depth:       template.Depth,
				Position:    template.Position,
			}
			// Resolve inactive arguments for diagnostics without turning a
			// skipped field into a reachable authorization failure.
			if arguments, argErr := evaluateArguments(template.Arguments, variables); argErr == nil {
				node.Arguments = arguments
				if pagination, pageErr := buildPagination(arguments); pageErr == nil {
					node.Pagination = pagination
				}
				node.NodeIDs = NodeIDsFromArguments(arguments)
			} else {
				node.Error = argErr.Error()
			}
			plan.Nodes = append(plan.Nodes, node)
			continue
		}
		arguments, err := evaluateArguments(template.Arguments, variables)
		if err != nil {
			invalidRuntime = true
			markDenied(template.Path)
			failures = append(failures, AuthFailure{
				Path:      template.Path,
				FieldName: template.FieldName,
				Alias:     template.Alias,
				NodeID:    template.ID,
				Reason:    err.Error(),
			})
			plan.Nodes = append(plan.Nodes, &AuthNode{
				ID:          template.ID,
				ParentID:    template.ParentID,
				TemplateID:  template.ID,
				Order:       template.Order,
				ParentType:  template.ParentType,
				FieldName:   template.FieldName,
				Alias:       template.Alias,
				ResponseKey: template.ResponseKey,
				Path:        template.Path.Clone(),
				Directives:  directives,
				Active:      active,
				IdentityKey: plan.IdentityKey,
				Error:       err.Error(),
				Depth:       template.Depth,
				Position:    template.Position,
			})
			continue
		}
		pagination, err := buildPagination(arguments)
		if err != nil {
			invalidRuntime = true
			markDenied(template.Path)
			failures = append(failures, AuthFailure{
				Path:      template.Path,
				FieldName: template.FieldName,
				Alias:     template.Alias,
				NodeID:    template.ID,
				Reason:    err.Error(),
			})
			plan.Nodes = append(plan.Nodes, &AuthNode{
				ID:          template.ID,
				ParentID:    template.ParentID,
				TemplateID:  template.ID,
				Order:       template.Order,
				ParentType:  template.ParentType,
				FieldName:   template.FieldName,
				Alias:       template.Alias,
				ResponseKey: template.ResponseKey,
				Path:        template.Path.Clone(),
				Arguments:   arguments,
				Directives:  directives,
				Active:      active,
				IdentityKey: plan.IdentityKey,
				Error:       err.Error(),
				Depth:       template.Depth,
				Position:    template.Position,
			})
			continue
		}

		nodeIDs := NodeIDsFromArguments(arguments)
		node := &AuthNode{
			ID:                template.ID,
			ParentID:          template.ParentID,
			TemplateID:        template.ID,
			Order:             template.Order,
			ParentType:        template.ParentType,
			FieldName:         template.FieldName,
			Alias:             template.Alias,
			ResponseKey:       template.ResponseKey,
			Path:              template.Path.Clone(),
			Arguments:         arguments,
			ArgumentSignature: canonicalArguments(template.Arguments),
			Pagination:        pagination,
			NodeIDs:           nodeIDs,
			Directives:        directives,
			Active:            active,
			IdentityKey:       plan.IdentityKey,
			Depth:             template.Depth,
			Position:          template.Position,
		}
		if active {
			decision, err := authorizeNode(ctx, policy, identity, plan.IdentityKey, static, template, node, plan.Variables, e != nil && e.DisableTenantIsolation)
			if err != nil {
				policyFailure = true
				decision = Deny(err.Error())
				node.Error = err.Error()
				failures = append(failures, AuthFailure{
					Path:      node.Path,
					FieldName: node.FieldName,
					Alias:     node.Alias,
					NodeID:    node.ID,
					Reason:    err.Error(),
					Decision:  decision,
				})
			} else if !decision.Allowed {
				if decision.Reason != "" {
					failures = append(failures, AuthFailure{
						Path:      node.Path,
						FieldName: node.FieldName,
						Alias:     node.Alias,
						NodeID:    node.ID,
						Reason:    decision.Reason,
						Decision:  decision,
					})
				} else {
					failures = append(failures, AuthFailure{
						Path:      node.Path,
						FieldName: node.FieldName,
						Alias:     node.Alias,
						NodeID:    node.ID,
						Reason:    "field denied",
						Decision:  decision,
					})
				}
				node.Decision = decision
				node.Authorized = false
			} else {
				node.Decision = decision
				node.Authorized = true
			}
			if err != nil {
				node.Decision = decision
				node.Authorized = false
			}
			if _, seen := activeByPath[node.Path.Key()]; !seen {
				pathOrder = append(pathOrder, node.Path.Clone())
			}
			activeByPath[node.Path.Key()] = append(activeByPath[node.Path.Key()], node)
		}
		plan.Nodes = append(plan.Nodes, node)
	}

	for _, path := range pathOrder {
		if _, alreadyDenied := denied[path.Key()]; alreadyDenied {
			continue
		}
		if hasDeniedAncestor(path, denied) {
			blockedOrder = append(blockedOrder, path.Clone())
			continue
		}
		group := activeByPath[path.Key()]
		if len(group) == 0 {
			continue
		}
		field := &ResponseField{
			Path:        path.Clone(),
			FieldName:   group[0].FieldName,
			ResponseKey: group[0].ResponseKey,
			ParentType:  group[0].ParentType,
			IdentityKey: group[0].IdentityKey,
			Decision:    group[0].Decision,
			Occurrences: len(group),
		}
		authorized := true
		conflictReason := ""
		for _, node := range group {
			field.Nodes = append(field.Nodes, node)
			if !node.Authorized {
				authorized = false
				continue
			}
			if node.FieldName != field.FieldName || node.ParentType != field.ParentType {
				conflictReason = "aliases at the same response path resolve to different fields"
				continue
			}
			if canonicalJSON(node.Arguments) != canonicalJSON(group[0].Arguments) {
				conflictReason = "aliases at the same response path have different arguments"
				continue
			}
			if node.ArgumentSignature != group[0].ArgumentSignature {
				conflictReason = "aliases at the same response path use different argument expressions"
				continue
			}
			if node.IdentityKey != field.IdentityKey {
				conflictReason = "aliases at the same response path have different identities"
				continue
			}
			if !node.Decision.Equal(field.Decision) {
				conflictReason = "aliases at the same response path have different authorization results"
			}
		}
		if !authorized {
			markDenied(path)
			continue
		}
		if conflictReason != "" {
			conflict = true
			markDenied(path)
			failures = append(failures, AuthFailure{
				Path:      path.Clone(),
				FieldName: group[0].FieldName,
				Alias:     group[0].Alias,
				NodeID:    group[0].ID,
				Reason:    conflictReason,
				Decision:  group[0].Decision,
			})
			continue
		}
		field.Merged = len(group) > 1
		plan.Fields = append(plan.Fields, field)
	}
	plan.ResponseFields = plan.Fields
	plan.DeniedPaths = deniedOrder
	plan.BlockedPaths = blockedOrder

	plan.AuthorizationDigest = authorizationDigest(plan)
	if len(failures) > 0 {
		plan.Failures = failures
		plan.Rejected = true
		causeErrors := []error{ErrUnauthorized}
		if conflict {
			causeErrors = append(causeErrors, ErrResponseConflict)
		}
		if policyFailure {
			causeErrors = append(causeErrors, ErrPolicy)
		}
		if invalidRuntime {
			causeErrors = append(causeErrors, ErrInvalidVariables)
		}
		message := authorizationMessage(failures)
		return plan, &AuthorizationError{
			Message:  message,
			Failures: failures,
			Cause:    errors.Join(causeErrors...),
		}
	}
	_ = batchID
	_ = persistedID
	return plan, nil
}

func hasDeniedAncestor(path Path, denied map[string]struct{}) bool {
	for length := 1; length < len(path); length++ {
		if _, exists := denied[path[:length].Key()]; exists {
			return true
		}
	}
	return false
}

func authorizeNode(ctx context.Context, policy Policy, identity Identity, identityKey string, static *StaticPlan, template *NodeTemplate, node *AuthNode, variables map[string]any, disableTenantIsolation bool) (Decision, error) {
	request := FieldRequest{
		Identity:            identity,
		IdentityKey:         identityKey,
		OperationName:       static.OperationName,
		OperationType:       static.Operation.Type,
		ParentType:          template.ParentType,
		FieldName:           template.FieldName,
		Alias:               template.Alias,
		ResponseKey:         template.ResponseKey,
		ResponsePath:        template.Path.Clone(),
		Path:                template.Path.Clone(),
		Arguments:           node.Arguments,
		Variables:           variables,
		Pagination:          node.Pagination,
		NodeIDs:             node.NodeIDs,
		Directives:          node.Directives,
		AuthorizationNodeID: template.ID,
		Occurrence:          template.Order,
		Depth:               template.Depth,
		Position:            template.Position,
	}
	var decision Decision
	var foreignTenant string
	if !disableTenantIsolation {
		mismatches := tenantArgumentMismatch(node.Arguments, identity.TenantID)
		if len(mismatches) > 0 {
			foreignTenant = mismatches[0]
		}
	}
	var err error
	decision, err = policy.AuthorizeField(ctx, request)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: %v", ErrPolicy, err)
	}
	if foreignTenant != "" {
		decision.Allowed = false
		decision.Reason = "cross-tenant node ID"
	}
	if decision.Allowed {
		if requested, ok := node.Pagination.RequestedSize(); ok && decision.MaxPageSize > 0 && requested > decision.MaxPageSize {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("requested page size %d exceeds authorization cap %d", requested, decision.MaxPageSize)
		}
	}
	return decision, nil
}

func authorizationDigest(plan *Plan) string {
	parts := []string{plan.Digest, plan.IdentityKey}
	for _, node := range plan.Nodes {
		parts = append(parts,
			node.ID,
			strconv.FormatBool(node.Active),
			canonicalJSON(node.Arguments),
			node.IdentityKey,
			node.Decision.key(),
		)
	}
	return digestString(parts...)
}

func authorizationMessage(failures []AuthFailure) string {
	seen := make(map[string]struct{}, len(failures))
	paths := make([]string, 0, len(failures))
	for _, failure := range failures {
		path := failure.Path.String()
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) > 0 {
		return fmt.Sprintf("netsec: authorization rejected response path(s): %s", joinReasons(paths))
	}
	return "netsec: authorization rejected"
}

func evaluateDirectives(conditions []PlannedDirective, variables map[string]any) ([]DirectiveState, bool, error) {
	states := make([]DirectiveState, 0, len(conditions))
	active := true
	for _, condition := range conditions {
		state := DirectiveState{
			Name:     condition.Name,
			Origin:   condition.Origin,
			Position: condition.Position,
			Active:   true,
		}
		switch condition.Name {
		case "skip", "include":
			if len(condition.Arguments) != 1 || condition.Arguments[0].Name != "if" {
				return states, false, fmt.Errorf("%w: directive @%s requires exactly one if argument", ErrInvalidVariables, condition.Name)
			}
			value, err := condition.Arguments[0].Value.Evaluate(variables)
			if err != nil {
				return states, false, fmt.Errorf("%w: @%s: %v", ErrInvalidVariables, condition.Name, err)
			}
			boolean, ok := value.(bool)
			if !ok {
				return states, false, fmt.Errorf("%w: @%s if argument must be Boolean, got %T", ErrInvalidVariables, condition.Name, value)
			}
			state.Evaluated = true
			state.Recognized = true
			state.Value = boolean
			if condition.Name == "skip" {
				state.Active = !boolean
			} else {
				state.Active = boolean
			}
			if !state.Active {
				active = false
			}
		default:
			state.Recognized = false
			for _, argument := range condition.Arguments {
				if _, err := argument.Value.Evaluate(variables); err != nil {
					return states, false, fmt.Errorf("%w: @%s: %v", ErrInvalidVariables, condition.Name, err)
				}
			}
		}
		states = append(states, state)
	}
	return states, active, nil
}

func evaluateArguments(arguments []*Argument, variables map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(arguments))
	for _, argument := range arguments {
		value, err := argument.Value.Evaluate(variables)
		if err != nil {
			return nil, fmt.Errorf("%w: argument %q: %v", ErrInvalidVariables, argument.Name, err)
		}
		result[argument.Name] = cloneJSONValue(value)
	}
	return result, nil
}

func resolveVariables(operation *Operation, provided map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(operation.VariableDefinitions))
	for _, definition := range operation.VariableDefinitions {
		value, exists := provided[definition.Name]
		if !exists {
			if definition.HasDefault {
				literal, err := definition.Default.Evaluate(map[string]any{})
				if err != nil {
					return nil, fmt.Errorf("%w: default for $%s: %v", ErrInvalidVariables, definition.Name, err)
				}
				value = literal
			} else {
				if definition.Type != nil && definition.Type.NonNull {
					return nil, fmt.Errorf("%w: required variable $%s was not provided", ErrInvalidVariables, definition.Name)
				}
				// A nullable omitted variable behaves as an explicitly absent
				// input. Keeping nil makes argument and directive evaluation
				// fail closed instead of silently widening a field request.
				value = nil
			}
		}
		coerced, err := coerceVariableValue(value, definition.Type, "$"+definition.Name)
		if err != nil {
			return nil, err
		}
		result[definition.Name] = cloneJSONValue(coerced)
	}
	return result, nil
}

func coerceVariableValue(value any, typeReference *TypeReference, path string) (any, error) {
	if typeReference == nil {
		return value, nil
	}
	if value == nil {
		if typeReference.NonNull {
			return nil, fmt.Errorf("%w: %s must not be null", ErrInvalidVariables, path)
		}
		return nil, nil
	}
	if typeReference.List != nil {
		var items []any
		switch typed := value.(type) {
		case []any:
			items = typed
		case []string:
			items = make([]any, len(typed))
			for i, item := range typed {
				items[i] = item
			}
		default:
			reflected := reflect.ValueOf(value)
			if reflected.IsValid() && (reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) {
				items = make([]any, reflected.Len())
				for i := 0; i < reflected.Len(); i++ {
					items[i] = reflected.Index(i).Interface()
				}
			} else {
				// GraphQL list coercion wraps a single value.
				items = []any{value}
			}
		}
		result := make([]any, len(items))
		for i, item := range items {
			coerced, err := coerceVariableValue(item, typeReference.List, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			result[i] = coerced
		}
		return result, nil
	}

	named := typeReference.Named
	switch named {
	case "Boolean":
		boolean, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: %s must be Boolean", ErrInvalidVariables, path)
		}
		return boolean, nil
	case "String":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s must be String", ErrInvalidVariables, path)
		}
		return text, nil
	case "ID":
		switch typed := value.(type) {
		case string:
			return typed, nil
		default:
			if integer, ok := asInt64(typed); ok {
				return strconv.FormatInt(integer, 10), nil
			}
			return nil, fmt.Errorf("%w: %s must be ID", ErrInvalidVariables, path)
		}
	case "Int":
		integer, ok := asInt64(value)
		if !ok {
			return nil, fmt.Errorf("%w: %s must be Int", ErrInvalidVariables, path)
		}
		return int(integer), nil
	case "Float":
		number, ok := asFloat64(value)
		if !ok {
			return nil, fmt.Errorf("%w: %s must be Float", ErrInvalidVariables, path)
		}
		return number, nil
	default:
		return value, nil
	}
}

func asInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		if uint64(typed) > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(typed), true
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(typed), true
	case float32:
		number := float64(typed)
		if number != float64(int64(number)) {
			return 0, false
		}
		return int64(number), true
	case float64:
		if typed != float64(int64(typed)) {
			return 0, false
		}
		return int64(typed), true
	case json.Number:
		integer, err := typed.Int64()
		return integer, err == nil
	default:
		return 0, false
	}
}

func asFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		if integer, ok := asInt64(value); ok {
			return float64(integer), true
		}
		return 0, false
	}
}

func buildPagination(arguments map[string]any) (Pagination, error) {
	var pagination Pagination
	itemBounds := 0
	for rawKey, value := range arguments {
		key := normalizeArgumentName(rawKey)
		switch key {
		case "first", "last", "limit", "pagesize":
		default:
			continue
		}
		integer, ok := asInt64(value)
		if !ok || integer < 0 {
			return pagination, fmt.Errorf("%w: pagination argument %s must be a non-negative integer", ErrInvalidVariables, rawKey)
		}
		itemBounds++
		switch key {
		case "first":
			pagination.First, pagination.HasFirst = integer, true
		case "last":
			pagination.Last, pagination.HasLast = integer, true
		case "limit":
			pagination.Limit, pagination.HasLimit = integer, true
		case "pagesize":
			pagination.PageSize, pagination.HasPageSize = integer, true
		}
	}
	if itemBounds > 1 {
		return pagination, fmt.Errorf("%w: multiple item-count pagination arguments would make authorization ambiguous", ErrInvalidVariables)
	}
	for rawKey, value := range arguments {
		key := normalizeArgumentName(rawKey)
		if key != "offset" && key != "page" {
			continue
		}
		integer, ok := asInt64(value)
		if !ok {
			return pagination, fmt.Errorf("%w: pagination argument %s must be an integer", ErrInvalidVariables, rawKey)
		}
		if key == "offset" {
			if integer < 0 {
				return pagination, fmt.Errorf("%w: offset must be non-negative", ErrInvalidVariables)
			}
			pagination.Offset, pagination.HasOffset = integer, true
		} else {
			if integer < 1 {
				return pagination, fmt.Errorf("%w: page must be at least 1", ErrInvalidVariables)
			}
			pagination.Page, pagination.HasPage = integer, true
		}
	}
	for rawKey, value := range arguments {
		key := normalizeArgumentName(rawKey)
		if key != "after" && key != "before" {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return pagination, fmt.Errorf("%w: pagination cursor %s must be a string", ErrInvalidVariables, rawKey)
		}
		if key == "after" {
			pagination.After, pagination.HasAfter = text, true
		} else {
			pagination.Before, pagination.HasBefore = text, true
		}
	}
	return pagination, nil
}

// AuthorizeQuery is a convenience entry point using default engine limits. An
// optional operation name may be supplied for multi-operation documents.
func AuthorizeQuery(ctx context.Context, query string, variables map[string]any, identity Identity, policy Policy, operationName ...string) (*Plan, error) {
	selectedOperation := ""
	if len(operationName) > 1 {
		return nil, fmt.Errorf("%w: at most one operation name may be supplied", ErrInvalidQuery)
	}
	if len(operationName) == 1 {
		selectedOperation = operationName[0]
	}
	return NewEngine(policy).Authorize(ctx, Request{
		Query:         query,
		OperationName: selectedOperation,
		Variables:     variables,
		Identity:      identity,
	})
}

// AuthorizeOperation authorizes a named operation in a possibly multi-operation
// document.
func AuthorizeOperation(ctx context.Context, query, operationName string, variables map[string]any, identity Identity, policy Policy) (*Plan, error) {
	return NewEngine(policy).Authorize(ctx, Request{
		Query:         query,
		OperationName: operationName,
		Variables:     variables,
		Identity:      identity,
	})
}
