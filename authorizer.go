package netsec

import "sync"

// Action identifies the kind of authorization being checked.
type Action string

const (
	// ActionSubscribe covers creating or retaining a subscription.
	ActionSubscribe Action = "subscribe"
	// ActionReceive covers delivering a message to a subscriber.
	ActionReceive Action = "receive"
	// ActionPublish covers accepting a message from a publisher.
	ActionPublish Action = "publish"
)

// Authorizer is consulted using the session's authenticated principal. The
// broker should make sure calls observe the current ACL, not a copy cached at
// CONNECT time.
//
// Authorize must not call back into the Manager that is using it; the Manager
// may hold its internal lock while invoking the ACL.
type Authorizer interface {
	Authorize(principal string, action Action, topic string) bool
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(principal string, action Action, topic string) bool

// Authorize implements Authorizer.
func (f AuthorizerFunc) Authorize(principal string, action Action, topic string) bool {
	return f(principal, action, topic)
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Authorize(string, Action, string) bool { return true }

type denyAllAuthorizer struct{}

func (denyAllAuthorizer) Authorize(string, Action, string) bool { return false }

// AllowAll authorizes every request. It is intended for tests or for brokers
// that enforce ACLs at a different layer.
var AllowAll Authorizer = allowAllAuthorizer{}

// DenyAll denies every request. It is the Manager default.
var DenyAll Authorizer = denyAllAuthorizer{}

// ACLRule is one decision in an ACL. The last rule that matches a request
// wins, so a deny rule appended after an allow rule revokes access.
type ACLRule struct {
	// Principal is matched exactly. "*" or an empty string matches any
	// principal.
	Principal string
	// Action is matched exactly. An empty string matches any action.
	Action Action
	// Topic is an MQTT topic filter used to match the requested filter or
	// topic. "*" or an empty string matches any topic. A "#" rule also
	// matches all ordinary topics.
	Topic string
	// Allow is the decision when the rule matches.
	Allow bool
}

// ACL is a concurrency-safe, last-match-wins authorizer. It is deliberately
// small, but is useful for wiring a broker and for exercising ACL changes at
// runtime.
type ACL struct {
	mu    sync.RWMutex
	rules []ACLRule
}

// NewACL returns an empty ACL. An empty ACL denies every request.
func NewACL() *ACL {
	return &ACL{}
}

// Add appends a rule. Later rules take precedence over earlier ones.
func (a *ACL) Add(rule ACLRule) {
	a.mu.Lock()
	a.rules = append(a.rules, rule)
	a.mu.Unlock()
}

// Allow appends an allow rule.
func (a *ACL) Allow(principal string, action Action, topic string) {
	a.Add(ACLRule{Principal: principal, Action: action, Topic: topic, Allow: true})
}

// Deny appends a deny rule.
func (a *ACL) Deny(principal string, action Action, topic string) {
	a.Add(ACLRule{Principal: principal, Action: action, Topic: topic, Allow: false})
}

// Revoke is an alias for Deny. Appending a deny after an allow revokes the
// permission for subsequent authorization checks.
func (a *ACL) Revoke(principal string, action Action, topic string) {
	a.Deny(principal, action, topic)
}

// Authorize implements Authorizer.
func (a *ACL) Authorize(principal string, action Action, topic string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	allowed := false
	for _, rule := range a.rules {
		if rule.Principal != "" && rule.Principal != "*" && rule.Principal != principal {
			continue
		}
		if rule.Action != "" && rule.Action != action {
			continue
		}
		if rule.Topic != "" && rule.Topic != "*" && !TopicMatches(rule.Topic, topic) {
			continue
		}
		allowed = rule.Allow
	}
	return allowed
}
