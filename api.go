package netsec

import "context"

// QueryPlan is an alias for the immutable query-shape plan.
type QueryPlan = StaticPlan

// AuthorizationPlan is an alias for the runtime authorization plan.
type AuthorizationPlan = Plan

// BuildPlan is an alias for Compile.
func BuildPlan(query string, operationName ...string) (*StaticPlan, error) {
	return Compile(query, operationName...)
}

// BuildAuthorizationPlan is an alias for BuildPlan.
func BuildAuthorizationPlan(query string, operationName ...string) (*StaticPlan, error) {
	return Compile(query, operationName...)
}

// NewQueryAuthorizer creates an authorization engine.
func NewQueryAuthorizer(policy Policy) *Engine {
	return NewEngine(policy)
}

// AuthorizeRequest authorizes one request with default engine limits.
func AuthorizeRequest(ctx context.Context, request Request, policy Policy) (*Plan, error) {
	return NewEngine(policy).Authorize(ctx, request)
}
