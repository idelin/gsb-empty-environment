package netsec

import (
	"fmt"
	"sync"
)

// BACnet priority array bounds. Levels 1 and 2 are reserved for life-safety
// applications and must never be accepted from the gateway.
const (
	PriorityHighest     uint8 = 1
	PriorityLowest      uint8 = 16
	MinWritablePriority uint8 = 3
	MaxWritablePriority uint8 = 16
)

// Config describes an admission policy. Its zero value is secure by default:
// no targets are registered, writes are accepted only at priorities 3 through
// 16, and all control services are denied.
type Config struct {
	// AllowedTargets is the exact set of object/property pairs that may be
	// written. The policy never matches wildcards.
	AllowedTargets []Target

	// MinPriority and MaxPriority bound the accepted priority values. Zero
	// selects the default (3 for MinPriority, 16 for MaxPriority). Values
	// below 3 are rejected because priorities 1 and 2 are reserved.
	MinPriority uint8
	MaxPriority uint8

	// AllowedControlServices is empty by default. Adding a control service
	// here is an explicit, dangerous opt-in; normal gateway deployments
	// should leave it empty.
	AllowedControlServices []Service
}

// Policy is a concurrency-safe BACnet write admission policy.
type Policy struct {
	mu       sync.RWMutex
	allowed  map[Target]struct{}
	controls map[Service]struct{}
	min      uint8
	max      uint8
}

// NewPolicy validates cfg and constructs a policy. It fails closed on an
// invalid configuration instead of silently broadening the allowlist or
// priority range.
func NewPolicy(cfg Config) (*Policy, error) {
	minPriority := cfg.MinPriority
	if minPriority == 0 {
		minPriority = MinWritablePriority
	}
	maxPriority := cfg.MaxPriority
	if maxPriority == 0 {
		maxPriority = MaxWritablePriority
	}

	if minPriority < MinWritablePriority {
		return nil, fmt.Errorf("%w: minimum priority %d: %w",
			ErrInvalidPolicy, minPriority, ErrReservedPriority)
	}
	if maxPriority > MaxWritablePriority {
		return nil, fmt.Errorf("%w: maximum priority %d exceeds %d",
			ErrInvalidPolicy, maxPriority, MaxWritablePriority)
	}
	if minPriority > maxPriority {
		return nil, fmt.Errorf("%w: minimum priority %d exceeds maximum %d",
			ErrInvalidPolicy, minPriority, maxPriority)
	}

	policy := &Policy{
		allowed:  make(map[Target]struct{}, len(cfg.AllowedTargets)),
		controls: make(map[Service]struct{}),
		min:      minPriority,
		max:      maxPriority,
	}

	for _, target := range cfg.AllowedTargets {
		if err := target.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidPolicy, err)
		}
		policy.allowed[target] = struct{}{}
	}
	for _, service := range cfg.AllowedControlServices {
		if !service.IsControlService() {
			return nil, fmt.Errorf("%w: service %s is not a control service",
				ErrInvalidPolicy, service)
		}
		policy.controls[service] = struct{}{}
	}

	return policy, nil
}

// Register adds exact object/property pairs to the allowlist. Registration is
// atomic: if any target is invalid, none are added.
func (p *Policy) Register(targets ...Target) error {
	if p == nil {
		return ErrInvalidPolicy
	}
	for _, target := range targets {
		if err := target.Validate(); err != nil {
			return err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.allowed == nil {
		p.allowed = make(map[Target]struct{}, len(targets))
	}
	for _, target := range targets {
		p.allowed[target] = struct{}{}
	}
	return nil
}

// Unregister removes an object/property pair from the allowlist.
func (p *Policy) Unregister(target Target) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.allowed, target)
	p.mu.Unlock()
}

// IsRegistered reports whether an exact object/property pair is allowed.
func (p *Policy) IsRegistered(target Target) bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	_, ok := p.allowed[target]
	p.mu.RUnlock()
	return ok
}

// PriorityRange returns the inclusive priority interval accepted by the
// policy.
func (p *Policy) PriorityRange() (minPriority, maxPriority uint8) {
	if p == nil {
		return 0, 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.min, p.max
}

// AllowControlService explicitly enables a control service. It is intended
// only for tightly controlled maintenance paths and is never enabled by
// default.
func (p *Policy) AllowControlService(service Service) error {
	if p == nil {
		return ErrInvalidPolicy
	}
	if !service.IsControlService() {
		return fmt.Errorf("%w: service %s is not a control service",
			ErrInvalidPolicy, service)
	}
	p.mu.Lock()
	if p.controls == nil {
		p.controls = make(map[Service]struct{})
	}
	p.controls[service] = struct{}{}
	p.mu.Unlock()
	return nil
}

// DenyControlService removes any explicit opt-in for a control service.
func (p *Policy) DenyControlService(service Service) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.controls, service)
	p.mu.Unlock()
}

// Admit evaluates req and returns a detailed decision. It fails closed for
// unknown services, unregistered targets, missing or out-of-range priorities,
// and control services that were not explicitly enabled.
func (p *Policy) Admit(req Request) Decision {
	if p == nil {
		return denied(ReasonMalformed, -1, "nil admission policy")
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	switch req.Service {
	case ServiceWriteProperty:
		return p.admitWrite(-1, req.Object, req.Property, req.Priority)
	case ServiceWritePropertyMultiple:
		return p.admitWriteMultiple(req.Writes)
	default:
		if req.Service.IsControlService() {
			if _, ok := p.controls[req.Service]; ok {
				return Decision{Allowed: true, Member: -1}
			}
			return denied(ReasonControlServiceDenied, -1,
				req.Service.String()+" is disabled")
		}
		return denied(ReasonUnknownService, -1, req.Service.String())
	}
}

// Authorize is the error-returning form of Admit for callers that prefer
// idiomatic Go error handling.
func (p *Policy) Authorize(req Request) error {
	return p.Admit(req).Err()
}

// Allows reports whether req passes admission.
func (p *Policy) Allows(req Request) bool {
	return p.Admit(req).Allowed
}

// Check is an alias for Admit for callers that prefer a policy-check name.
func (p *Policy) Check(req Request) Decision {
	return p.Admit(req)
}

func (p *Policy) admitWrite(member int, object ObjectID, property PropertyID, priority uint8) Decision {
	violation, ok := p.checkWrite(member, object, property, priority)
	if !ok {
		return Decision{
			Allowed:    false,
			Reason:     violation.Reason,
			Member:     member,
			Detail:     violation.Detail,
			Violations: []Violation{violation},
		}
	}
	return Decision{Allowed: true, Member: -1}
}

func (p *Policy) admitWriteMultiple(writes []WriteAccess) Decision {
	if len(writes) == 0 {
		return denied(ReasonEmptyWriteList, -1,
			"WritePropertyMultiple has no write access specifications")
	}

	var violations []Violation
	for i, write := range writes {
		violation, ok := p.checkWrite(i, write.Object, write.Property, write.Priority)
		if !ok {
			violations = append(violations, violation)
		}
	}
	if len(violations) == 0 {
		return Decision{Allowed: true, Member: -1}
	}

	return Decision{
		Allowed:    false,
		Reason:     violations[0].Reason,
		Member:     violations[0].Member,
		Detail:     violations[0].Detail,
		Violations: violations,
	}
}

func (p *Policy) checkWrite(member int, object ObjectID, property PropertyID, priority uint8) (Violation, bool) {
	detail := func(reason Reason, format string, args ...any) (Violation, bool) {
		return Violation{
			Member: member,
			Reason: reason,
			Detail: fmt.Sprintf(format, args...),
		}, false
	}

	if err := object.Validate(); err != nil {
		return detail(ReasonMalformed, "%v", err)
	}
	if err := property.Validate(); err != nil {
		return detail(ReasonMalformed, "%v", err)
	}

	target := Target{Object: object, Property: property}
	if _, ok := p.allowed[target]; !ok {
		return detail(ReasonUnregisteredTarget, "%s is not registered", target)
	}

	if reason, message := p.checkPriority(priority); reason != ReasonAllowed {
		return detail(reason, "%s", message)
	}
	return Violation{}, true
}

func (p *Policy) checkPriority(priority uint8) (Reason, string) {
	switch {
	case priority == 0:
		return ReasonMissingPriority, "write has no priority"
	case priority < MinWritablePriority:
		return ReasonPriorityReserved,
			fmt.Sprintf("priority %d is reserved (minimum is %d)", priority, MinWritablePriority)
	case priority > MaxWritablePriority:
		return ReasonPriorityOutOfRange,
			fmt.Sprintf("priority %d exceeds %d", priority, MaxWritablePriority)
	case priority < p.min || priority > p.max:
		return ReasonPriorityOutOfRange,
			fmt.Sprintf("priority %d is outside allowed range [%d, %d]", priority, p.min, p.max)
	default:
		return ReasonAllowed, ""
	}
}

func denied(reason Reason, member int, detail string) Decision {
	return Decision{
		Allowed: false,
		Reason:  reason,
		Member:  member,
		Detail:  detail,
		Violations: []Violation{{
			Member: member,
			Reason: reason,
			Detail: detail,
		}},
	}
}
