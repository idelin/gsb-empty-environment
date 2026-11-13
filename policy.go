package netsec

import "strings"

const (
	// ActionPassRole is the action required to attach a role to a service.
	ActionPassRole = "iam:PassRole"
	// ActionCreateFunction is required to create a Lambda function.
	ActionCreateFunction = "lambda:CreateFunction"
	// ActionInvokeFunction is required to execute a Lambda function.
	ActionInvokeFunction = "lambda:InvokeFunction"

	// ServiceLambda is the Lambda service principal used in trust policies
	// and in the iam:PassedToService condition.
	ServiceLambda = "lambda.amazonaws.com"

	// ConditionPassedToService is the condition key modeled by this package.
	ConditionPassedToService = "iam:PassedToService"
)

// Effect is an IAM statement effect.
type Effect string

const (
	// EffectAllow grants the matching actions.
	EffectAllow Effect = "Allow"
	// EffectDeny overrides any Allow for the matching actions.
	EffectDeny Effect = "Deny"
)

// Conditions holds the condition keys interpreted by this package.
//
// PassedToService restricts a statement to the listed service principals.
// Wildcards are supported. An empty slice means the statement is not
// service-scoped and applies to every service.
type Conditions struct {
	PassedToService []string
}

// Statement is a single identity-policy statement. Action and Resource may
// use the "*" and "?" IAM wildcard characters.
type Statement struct {
	Effect     Effect
	Actions    []string
	Resources  []string
	Conditions Conditions
}

// Policy is an ordered collection of statements attached to a principal or
// to a role. Multiple policies are merged when evaluating an identity.
type Policy struct {
	Statements []Statement
}

// TrustStatement is one statement of a role trust policy.
type TrustStatement struct {
	Effect     Effect
	Services   []string
	Conditions Conditions
}

// TrustPolicy describes which service principals may assume a role.
type TrustPolicy struct {
	Statements []TrustStatement
}

// Role is a passable role with its attached identity policies and its trust
// policy.
type Role struct {
	ARN      string
	Policies []Policy
	Trust    TrustPolicy
}

// globMatch reports whether value matches an IAM-style glob where "*" matches
// any run of characters and "?" matches exactly one character.
func globMatch(pattern, value string) bool {
	for pattern != "" {
		switch pattern[0] {
		case '*':
			if pattern == "*" {
				return true
			}
			for i := 0; i <= len(value); i++ {
				if globMatch(pattern[1:], value[i:]) {
					return true
				}
			}
			return false
		case '?':
			if value == "" {
				return false
			}
			pattern = pattern[1:]
			value = value[1:]
		default:
			if value == "" || pattern[0] != value[0] {
				return false
			}
			pattern = pattern[1:]
			value = value[1:]
		}
	}
	return value == ""
}

func actionMatches(pattern, action string) bool {
	return globMatch(strings.ToLower(pattern), strings.ToLower(action))
}

func anyPattern(patterns []string, match func(string) bool) bool {
	for _, pattern := range patterns {
		if match(pattern) {
			return true
		}
	}
	return false
}

// conditionAllowsService checks iam:PassedToService. An empty condition means
// no service restriction.
func conditionAllowsService(conditions Conditions, service string) bool {
	if len(conditions.PassedToService) == 0 {
		return true
	}
	return anyPattern(conditions.PassedToService, func(pattern string) bool {
		return globMatch(pattern, service)
	})
}

// statementCoversPass reports whether a statement matches PassRole for the
// given role resource and target service.
func statementCoversPass(statement Statement, roleARN, service string) bool {
	if !anyPattern(statement.Actions, func(pattern string) bool {
		return actionMatches(pattern, ActionPassRole)
	}) {
		return false
	}
	if !anyPattern(statement.Resources, func(pattern string) bool {
		return globMatch(pattern, roleARN)
	}) {
		return false
	}
	return conditionAllowsService(statement.Conditions, service)
}

// CanPassRole applies explicit-Deny-first semantics across all policies of an
// identity. It reports whether the identity may pass roleARN to service.
func CanPassRole(policies []Policy, roleARN, service string) bool {
	allowed := false
	for _, policy := range policies {
		for _, statement := range policy.Statements {
			if !statementCoversPass(statement, roleARN, service) {
				continue
			}
			switch statement.Effect {
			case EffectDeny:
				return false
			case EffectAllow:
				allowed = true
			}
		}
	}
	return allowed
}

// statementCoversAction checks action coverage without considering resources,
// used for Lambda permissions where the function does not exist yet.
func statementCoversAction(statement Statement, action string) bool {
	return anyPattern(statement.Actions, func(pattern string) bool {
		return actionMatches(pattern, action)
	})
}

func identityAllowsAction(policies []Policy, action string) bool {
	allowed := false
	for _, policy := range policies {
		for _, statement := range policy.Statements {
			if !statementCoversAction(statement, action) {
				continue
			}
			switch statement.Effect {
			case EffectDeny:
				return false
			case EffectAllow:
				allowed = true
			}
		}
	}
	return allowed
}

// CanCreateAndInvokeLambda reports whether the identity holds both
// lambda:CreateFunction and lambda:InvokeFunction, with Deny winning.
func CanCreateAndInvokeLambda(policies []Policy) bool {
	return identityAllowsAction(policies, ActionCreateFunction) &&
		identityAllowsAction(policies, ActionInvokeFunction)
}

func trustPrincipalMatches(statement TrustStatement, service string) bool {
	if !anyPattern(statement.Services, func(pattern string) bool {
		return globMatch(pattern, service)
	}) {
		return false
	}
	return conditionAllowsService(statement.Conditions, service)
}

// AllowsService evaluates a trust policy with explicit-Deny-first semantics.
func (trust TrustPolicy) AllowsService(service string) bool {
	allowed := false
	for _, statement := range trust.Statements {
		if !trustPrincipalMatches(statement, service) {
			continue
		}
		switch statement.Effect {
		case EffectDeny:
			return false
		case EffectAllow:
			allowed = true
		}
	}
	return allowed
}

// TrustsService reports whether the role may be assumed by service.
func (role Role) TrustsService(service string) bool {
	return role.Trust.AllowsService(service)
}
