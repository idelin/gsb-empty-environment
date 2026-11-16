package netsec

import "fmt"

// BACnet confirmed-service choices relevant to gateway admission.
const (
	ServiceWriteProperty              Service = 15
	ServiceWritePropertyMultiple      Service = 16
	ServiceDeviceCommunicationControl Service = 17
	ServiceReinitializeDevice         Service = 20
)

// Common BACnet object types. They are untyped constants so they can be used
// directly in ObjectID literals.
const (
	ObjectAnalogInput      = 0
	ObjectAnalogOutput     = 1
	ObjectAnalogValue      = 2
	ObjectBinaryInput      = 3
	ObjectBinaryOutput     = 4
	ObjectBinaryValue      = 5
	ObjectCalendar         = 6
	ObjectCommand          = 7
	ObjectDevice           = 8
	ObjectMultiStateInput  = 13
	ObjectMultiStateOutput = 14
	ObjectSchedule         = 17
	ObjectMultiStateValue  = 19
)

// Common BACnet property identifiers.
const (
	PropertyObjectIdentifier  PropertyID = 75
	PropertyObjectName        PropertyID = 77
	PropertyPresentValue      PropertyID = 85
	PropertyPriorityArray     PropertyID = 87
	PropertyRelinquishDefault PropertyID = 104
)

const (
	// FirstReservedPriority and LastReservedPriority are the two BACnet
	// priorities reserved for life-safety and other protected uses.
	FirstReservedPriority uint8 = 1
	LastReservedPriority  uint8 = 2

	// FirstUnreservedPriority is the lowest priority an untrusted caller may
	// use.
	FirstUnreservedPriority uint8 = 3

	// MaxBACnetPriority is the highest valid BACnet write priority.
	MaxBACnetPriority uint8 = 16

	// DefaultMinPriority and DefaultMaxPriority form the default permitted
	// priority interval.
	DefaultMinPriority uint8 = FirstUnreservedPriority
	DefaultMaxPriority uint8 = MaxBACnetPriority
)

const (
	maxObjectType     uint32 = 0x3ff
	maxObjectInstance uint32 = 0x3fffff
	maxPropertyID     uint32 = 0x3fffff
)

// Service identifies a BACnet confirmed service.
type Service uint8

// String returns a stable name for logging.
func (s Service) String() string {
	switch s {
	case ServiceWriteProperty:
		return "WriteProperty"
	case ServiceWritePropertyMultiple:
		return "WritePropertyMultiple"
	case ServiceDeviceCommunicationControl:
		return "DeviceCommunicationControl"
	case ServiceReinitializeDevice:
		return "ReinitializeDevice"
	default:
		return fmt.Sprintf("service(%d)", uint8(s))
	}
}

// ObjectID is a BACnet object identifier. Type occupies the 10-bit object
// type field and Instance occupies the 22-bit instance number field.
type ObjectID struct {
	Type     uint16
	Instance uint32
}

// String returns a stable representation for logs and errors.
func (o ObjectID) String() string {
	return fmt.Sprintf("object(%d):%d", o.Type, o.Instance)
}

// PropertyID is a BACnet property identifier.
type PropertyID uint32

// String returns a stable representation for logs and errors.
func (p PropertyID) String() string {
	return fmt.Sprintf("property(%d)", uint32(p))
}

// Write is one object/property/priority triple in a write request. Value is
// carried for the forwarding layer's convenience; admission decisions never
// depend on the encoded value.
type Write struct {
	Object   ObjectID
	Property PropertyID
	Priority uint8
	Value    any
}

// Request is the decoded service request presented to a Policy. A
// WriteProperty request must contain exactly one Write. A
// WritePropertyMultiple request may contain several.
type Request struct {
	Service Service
	Writes  []Write
}

// Rule registers one writable object/property pair. There is deliberately no
// wildcard form: a caller must register every pair it needs.
type Rule struct {
	Object   ObjectID
	Property PropertyID
}

// Option customizes a Policy at construction time.
type Option func(*policyConfig) error

type policyConfig struct {
	minPriority uint8
	maxPriority uint8
}

// WithPriorityRange narrows the permitted priority interval. The interval must
// be non-empty, stay within 3..16, and therefore can never include the
// reserved priorities 1 and 2.
func WithPriorityRange(minPriority, maxPriority uint8) Option {
	return func(c *policyConfig) error {
		if minPriority < FirstUnreservedPriority ||
			maxPriority > MaxBACnetPriority ||
			minPriority > maxPriority {
			return fmt.Errorf(
				"%w: interval %d..%d must stay within %d..%d",
				ErrInvalidPriorityRange,
				minPriority,
				maxPriority,
				FirstUnreservedPriority,
				MaxBACnetPriority,
			)
		}
		c.minPriority = minPriority
		c.maxPriority = maxPriority
		return nil
	}
}

// WithAllowedPriorityRange is an alias for WithPriorityRange.
func WithAllowedPriorityRange(minPriority, maxPriority uint8) Option {
	return WithPriorityRange(minPriority, maxPriority)
}

// Policy is an immutable admission policy. It is safe for concurrent use by a
// forwarding loop after construction.
type Policy struct {
	allowed     map[ObjectID]map[PropertyID]struct{}
	minPriority uint8
	maxPriority uint8
	ruleCount   int
}

// Admission is an alias for Policy.
type Admission = Policy

// NewPolicy builds a policy from an explicit set of writable object/property
// pairs. Duplicate rules are harmless. An empty rule set is valid and denies
// every write.
//
// The default priority interval is 3..16. Control services are never enabled
// by construction; only WriteProperty and WritePropertyMultiple can pass.
func NewPolicy(rules []Rule, opts ...Option) (*Policy, error) {
	cfg := policyConfig{
		minPriority: DefaultMinPriority,
		maxPriority: DefaultMaxPriority,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	p := &Policy{
		allowed:     make(map[ObjectID]map[PropertyID]struct{}, len(rules)),
		minPriority: cfg.minPriority,
		maxPriority: cfg.maxPriority,
	}

	for i, rule := range rules {
		if err := checkObjectID(rule.Object); err != nil {
			return nil, fmt.Errorf("%w at index %d: %w", ErrInvalidRule, i, err)
		}
		if uint32(rule.Property) > maxPropertyID {
			return nil, fmt.Errorf(
				"%w at index %d: property identifier %d exceeds %d",
				ErrInvalidRule,
				i,
				uint32(rule.Property),
				maxPropertyID,
			)
		}

		properties := p.allowed[rule.Object]
		if properties == nil {
			properties = make(map[PropertyID]struct{})
			p.allowed[rule.Object] = properties
		}
		if _, exists := properties[rule.Property]; !exists {
			properties[rule.Property] = struct{}{}
			p.ruleCount++
		}
	}

	return p, nil
}

// NewAdmission is an alias for NewPolicy.
func NewAdmission(rules []Rule, opts ...Option) (*Policy, error) {
	return NewPolicy(rules, opts...)
}

// NewWritePropertyRequest builds a single-target WriteProperty request.
func NewWritePropertyRequest(object ObjectID, property PropertyID, priority uint8, value any) Request {
	return Request{
		Service: ServiceWriteProperty,
		Writes: []Write{{
			Object:   object,
			Property: property,
			Priority: priority,
			Value:    value,
		}},
	}
}

// NewWritePropertyMultipleRequest builds a WritePropertyMultiple request. The
// policy evaluates every entry and accepts or rejects the request as a whole.
func NewWritePropertyMultipleRequest(writes ...Write) Request {
	return Request{
		Service: ServiceWritePropertyMultiple,
		Writes:  append([]Write(nil), writes...),
	}
}

// Admit decides whether a decoded request may be forwarded. A nil error means
// the entire request is allowed. Any error means nothing from the request may
// be applied.
//
// WritePropertyMultiple is atomic: all entries are checked before success is
// returned. A malformed WriteProperty carrying more than one entry is rejected
// rather than silently accepting only the first one.
func (p *Policy) Admit(req Request) error {
	if p == nil {
		return ErrNotConfigured
	}

	switch req.Service {
	case ServiceWriteProperty:
		if len(req.Writes) != 1 {
			return fmt.Errorf(
				"%w: WriteProperty requires exactly one target, got %d",
				ErrMalformedRequest,
				len(req.Writes),
			)
		}
	case ServiceWritePropertyMultiple:
		if len(req.Writes) == 0 {
			return fmt.Errorf(
				"%w: WritePropertyMultiple requires at least one target",
				ErrMalformedRequest,
			)
		}
	case ServiceDeviceCommunicationControl, ServiceReinitializeDevice:
		return fmt.Errorf(
			"%w: %w: %w: %s",
			ErrControlServiceDenied,
			ErrServiceDenied,
			ErrDenied,
			req.Service,
		)
	default:
		return fmt.Errorf("%w: %w: %s", ErrServiceDenied, ErrDenied, req.Service)
	}

	for i, write := range req.Writes {
		if err := p.checkWrite(write); err != nil {
			if req.Service == ServiceWritePropertyMultiple {
				return fmt.Errorf("WritePropertyMultiple entry %d: %w", i, err)
			}
			return fmt.Errorf("WriteProperty: %w", err)
		}
	}

	return nil
}

// Check is an alias for Admit.
func (p *Policy) Check(req Request) error {
	return p.Admit(req)
}

// Authorize is an alias for Admit.
func (p *Policy) Authorize(req Request) error {
	return p.Admit(req)
}

// AdmitWriteProperty is a convenience wrapper for a single-target write.
func (p *Policy) AdmitWriteProperty(object ObjectID, property PropertyID, priority uint8) error {
	return p.Admit(NewWritePropertyRequest(object, property, priority, nil))
}

// AdmitWritePropertyMultiple is a convenience wrapper for a write batch. It
// applies the same all-or-nothing rule as Admit.
func (p *Policy) AdmitWritePropertyMultiple(writes ...Write) error {
	return p.Admit(NewWritePropertyMultipleRequest(writes...))
}

// Allows reports whether an object/property pair is registered. It does not
// consider priority or service restrictions.
func (p *Policy) Allows(object ObjectID, property PropertyID) bool {
	if p == nil {
		return false
	}
	properties, ok := p.allowed[object]
	if !ok {
		return false
	}
	_, ok = properties[property]
	return ok
}

// PriorityRange returns the configured inclusive priority interval.
func (p *Policy) PriorityRange() (minPriority, maxPriority uint8) {
	if p == nil {
		return 0, 0
	}
	return p.minPriority, p.maxPriority
}

// RuleCount returns the number of distinct registered object/property pairs.
func (p *Policy) RuleCount() int {
	if p == nil {
		return 0
	}
	return p.ruleCount
}

func (p *Policy) checkWrite(write Write) error {
	if err := checkObjectID(write.Object); err != nil {
		return err
	}
	if uint32(write.Property) > maxPropertyID {
		return fmt.Errorf(
			"%w: property identifier %d exceeds %d",
			ErrMalformedRequest,
			uint32(write.Property),
			maxPropertyID,
		)
	}

	switch {
	case write.Priority == 0:
		return fmt.Errorf(
			"%w: %w: %w: %w: no priority supplied",
			ErrPriorityRequired,
			ErrPriorityOutOfRange,
			ErrPriorityDenied,
			ErrDenied,
		)
	case write.Priority <= LastReservedPriority:
		return fmt.Errorf(
			"%w: %w: %w: %w: priority %d is reserved (allowed %d..%d)",
			ErrPriorityReserved,
			ErrPriorityOutOfRange,
			ErrPriorityDenied,
			ErrDenied,
			write.Priority,
			p.minPriority,
			p.maxPriority,
		)
	case write.Priority < p.minPriority || write.Priority > p.maxPriority:
		return fmt.Errorf(
			"%w: %w: %w: priority %d is outside allowed interval %d..%d",
			ErrPriorityOutOfRange,
			ErrPriorityDenied,
			ErrDenied,
			write.Priority,
			p.minPriority,
			p.maxPriority,
		)
	}

	properties, ok := p.allowed[write.Object]
	if !ok {
		return fmt.Errorf(
			"%w: %w: %w: %s",
			ErrObjectNotAllowed,
			ErrTargetNotAllowed,
			ErrDenied,
			write.Object,
		)
	}
	if _, ok := properties[write.Property]; !ok {
		return fmt.Errorf(
			"%w: %w: %w: %s on %s",
			ErrPropertyNotAllowed,
			ErrTargetNotAllowed,
			ErrDenied,
			write.Property,
			write.Object,
		)
	}

	return nil
}

func checkObjectID(object ObjectID) error {
	if uint32(object.Type) > maxObjectType {
		return fmt.Errorf(
			"%w: object type %d exceeds %d",
			ErrMalformedRequest,
			object.Type,
			maxObjectType,
		)
	}
	if object.Instance > maxObjectInstance {
		return fmt.Errorf(
			"%w: object instance %d exceeds %d",
			ErrMalformedRequest,
			object.Instance,
			maxObjectInstance,
		)
	}
	return nil
}
