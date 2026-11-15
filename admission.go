package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// BACnet priority levels 1..16. Levels 1 and 2 are reserved for
// life-safety / manual operations and must never be granted to a
// forwarded network writer.
const (
	PriorityMin        = 1
	PriorityMax        = 16
	PriorityUnassigned = 0 // BACnet "no priority" (relinquish)
	DefaultMinPriority = 3 // lowest level allowed by a fresh policy

	PropPresentValue = 85 // PROP_PRESENT_VALUE
)

// ObjectInstanceMax is the largest valid BACnet object instance (22 bits).
const ObjectInstanceMax = 0x3FFFFF

// ObjectType is a BACnet object type identifier (0..1023).
type ObjectType uint16

// Common BACnet object types, exposed for readable policy construction.
const (
	ObjectAnalogInput      ObjectType = 0
	ObjectAnalogOutput     ObjectType = 1
	ObjectAnalogValue      ObjectType = 2
	ObjectBinaryInput      ObjectType = 3
	ObjectBinaryOutput     ObjectType = 4
	ObjectBinaryValue      ObjectType = 5
	ObjectMultiStateInput  ObjectType = 13
	ObjectMultiStateOutput ObjectType = 14
	ObjectMultiStateValue  ObjectType = 19
)

// Service selects which BACnet confirmed service a request is using.
type Service int

const (
	ServiceUnknown Service = iota
	ServiceWriteProperty
	ServiceWritePropertyMultiple
	ServiceDeviceCommunicationControl
	ServiceReinitializeDevice
)

// CallerID identifies the party originating a request (e.g. upstream
// BACnet/IP address or an authenticated principal).
type CallerID string

// ObjectID addresses a BACnet object.
type ObjectID struct {
	Type     ObjectType
	Instance uint32
}

// Valid reports whether the object identifier is inside the BACnet range.
func (o ObjectID) Valid() bool {
	return o.Instance <= ObjectInstanceMax && o.Type <= 1023
}

func (o ObjectID) String() string {
	return fmt.Sprintf("object(type=%d,instance=%d)", o.Type, o.Instance)
}

// WriteItem is one property write carried by WriteProperty or
// WritePropertyMultiple. Priority is the BACnet priority array level;
// PriorityUnassigned means no level was given.
type WriteItem struct {
	Object   ObjectID
	Property uint32
	Priority uint8
}

// WriteRequest is a single WriteProperty service request.
type WriteRequest struct {
	Caller   CallerID
	Object   ObjectID
	Property uint32
	Priority uint8
}

// WriteMultipleRequest is a WritePropertyMultiple service request.
// Admission is all-or-nothing: every item must pass or the whole
// request is rejected.
type WriteMultipleRequest struct {
	Caller CallerID
	Items  []WriteItem
}

// ControlRequest covers the device-level control services that can
// silence or restart a device. They are denied unless explicitly
// enabled for the caller.
type ControlRequest struct {
	Caller  CallerID
	Service Service
}

// Sentinel errors returned by the guard. Callers can use errors.Is.
var (
	ErrUnknownCaller      = errors.New("bacnet admission: caller is not registered")
	ErrObjectNotAllowed   = errors.New("bacnet admission: object not registered for caller")
	ErrPropertyNotAllowed = errors.New("bacnet admission: property not registered for object")
	ErrPriorityOutOfRange = errors.New("bacnet admission: write priority outside allowed range")
	ErrReservedPriority   = errors.New("bacnet admission: priority level 1 or 2 is reserved")
	ErrInvalidObjectID    = errors.New("bacnet admission: invalid object identifier")
	ErrControlDenied      = errors.New("bacnet admission: control service denied by default")
	ErrEmptyWriteMultiple = errors.New("bacnet admission: WritePropertyMultiple carries no items")
	ErrUnsupportedService = errors.New("bacnet admission: unsupported service")
	ErrInvalidPolicy      = errors.New("bacnet admission: invalid policy")
)

// ItemViolation explains why a single WritePropertyMultiple item failed.
type ItemViolation struct {
	Index int
	Item  WriteItem
	Err   error
}

// BatchViolation aggregates every rejected item in a
// WritePropertyMultiple request. It wraps the first underlying sentinel
// error so errors.Is keeps working.
type BatchViolation struct {
	Violations []ItemViolation
	err        error
}

func (b *BatchViolation) Error() string {
	if len(b.Violations) == 0 {
		return "bacnet admission: batch rejected"
	}
	v := b.Violations[0]
	return fmt.Sprintf("bacnet admission: batch rejected at item %d (%s, property %d, priority %d): %v",
		v.Index, v.Item.Object, v.Item.Property, v.Item.Priority, v.Err)
}

func (b *BatchViolation) Unwrap() error { return b.err }

// Policy declares what one caller may write.
//
// MinPriority..MaxPriority is the inclusive BACnet priority band the
// caller may use. It may never include levels 1 or 2. Objects maps each
// allowed object to the set of property identifiers writable on it.
type Policy struct {
	MinPriority uint8
	MaxPriority uint8
	Objects     map[ObjectID]map[uint32]struct{}
	// AllowDeviceCommunicationControl / AllowReinitializeDevice opt in
	// to the otherwise denied control services.
	AllowDeviceCommunicationControl bool
	AllowReinitializeDevice         bool
}

// Guard is the admission checker invoked by the forwarding loop. It is
// safe for concurrent use; policies are mutable through Register.
type Guard struct {
	mu       sync.RWMutex
	policies map[CallerID]Policy
}

// NewGuard returns an empty guard: every caller is denied until
// registered.
func NewGuard() *Guard {
	return &Guard{policies: make(map[CallerID]Policy)}
}

// Register installs or replaces the admission policy for a caller.
func (g *Guard) Register(caller CallerID, policy Policy) error {
	if caller == "" {
		return fmt.Errorf("%w: empty caller id", ErrInvalidPolicy)
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.policies[caller] = clonePolicy(policy)
	return nil
}

// Remove deletes a caller's policy, causing subsequent requests from it
// to be rejected with ErrUnknownCaller.
func (g *Guard) Remove(caller CallerID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.policies, caller)
}

func validatePolicy(p Policy) error {
	if p.MinPriority < PriorityMin || p.MaxPriority > PriorityMax || p.MinPriority > p.MaxPriority {
		return fmt.Errorf("%w: priority band %d..%d outside BACnet levels 1..16",
			ErrInvalidPolicy, p.MinPriority, p.MaxPriority)
	}
	if p.MinPriority <= 2 || p.MaxPriority <= 2 {
		return fmt.Errorf("%w: levels 1 and 2 are reserved", ErrInvalidPolicy)
	}
	for object, props := range p.Objects {
		if !object.Valid() {
			return fmt.Errorf("%w: %s", ErrInvalidPolicy, object)
		}
		if len(props) == 0 {
			return fmt.Errorf("%w: %s has no allowed properties", ErrInvalidPolicy, object)
		}
	}
	return nil
}

func clonePolicy(p Policy) Policy {
	cp := Policy{
		MinPriority:                     p.MinPriority,
		MaxPriority:                     p.MaxPriority,
		AllowDeviceCommunicationControl: p.AllowDeviceCommunicationControl,
		AllowReinitializeDevice:         p.AllowReinitializeDevice,
		Objects:                         make(map[ObjectID]map[uint32]struct{}, len(p.Objects)),
	}
	for object, props := range p.Objects {
		clone := make(map[uint32]struct{}, len(props))
		for prop := range props {
			clone[prop] = struct{}{}
		}
		cp.Objects[object] = clone
	}
	return cp
}

// AdmitWrite gates a single WriteProperty request.
func (g *Guard) AdmitWrite(req WriteRequest) error {
	g.mu.RLock()
	policy, ok := g.policies[req.Caller]
	g.mu.RUnlock()
	if !ok {
		return ErrUnknownCaller
	}
	return g.checkItem(policy, WriteItem{Object: req.Object, Property: req.Property, Priority: req.Priority})
}

// AdmitWriteMultiple gates a WritePropertyMultiple request. Any single
// violating item rejects the whole request; nothing should be forwarded.
func (g *Guard) AdmitWriteMultiple(req WriteMultipleRequest) error {
	if len(req.Items) == 0 {
		return ErrEmptyWriteMultiple
	}
	g.mu.RLock()
	policy, ok := g.policies[req.Caller]
	g.mu.RUnlock()
	if !ok {
		return ErrUnknownCaller
	}
	var violations []ItemViolation
	for index, item := range req.Items {
		if err := g.checkItem(policy, item); err != nil {
			violations = append(violations, ItemViolation{Index: index, Item: item, Err: err})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return &BatchViolation{Violations: violations, err: violations[0].Err}
}

// AdmitControl gates DeviceCommunicationControl and ReinitializeDevice.
// Both are denied unless the caller's policy explicitly enables them.
func (g *Guard) AdmitControl(req ControlRequest) error {
	g.mu.RLock()
	policy, ok := g.policies[req.Caller]
	g.mu.RUnlock()
	if !ok {
		return ErrUnknownCaller
	}
	switch req.Service {
	case ServiceDeviceCommunicationControl:
		if policy.AllowDeviceCommunicationControl {
			return nil
		}
		return ErrControlDenied
	case ServiceReinitializeDevice:
		if policy.AllowReinitializeDevice {
			return nil
		}
		return ErrControlDenied
	default:
		return fmt.Errorf("%w: service %d", ErrUnsupportedService, req.Service)
	}
}

// checkItem applies object, property and priority checks to one write.
func (g *Guard) checkItem(policy Policy, item WriteItem) error {
	if !item.Object.Valid() {
		return fmt.Errorf("%w: %s", ErrInvalidObjectID, item.Object)
	}
	properties, ok := policy.Objects[item.Object]
	if !ok {
		return fmt.Errorf("%w: %s for caller", ErrObjectNotAllowed, item.Object)
	}
	if _, ok := properties[item.Property]; !ok {
		return fmt.Errorf("%w: property %d on %s", ErrPropertyNotAllowed, item.Property, item.Object)
	}
	return checkPriority(item.Priority, policy)
}

// checkPriority enforces the BACnet range, the reserved 1/2 levels and
// the caller-specific allowed band. PriorityUnassigned (0) means the
// request carries no level and is accepted as long as the object and
// property are allowed.
func checkPriority(priority uint8, policy Policy) error {
	switch {
	case priority == PriorityUnassigned:
		return nil
	case priority < PriorityMin || priority > PriorityMax:
		return fmt.Errorf("%w: level %d is not a BACnet priority (1..16)", ErrPriorityOutOfRange, priority)
	case priority <= 2:
		return fmt.Errorf("%w: level %d", ErrReservedPriority, priority)
	case priority < policy.MinPriority || priority > policy.MaxPriority:
		return fmt.Errorf("%w: level %d outside allowed band %d..%d",
			ErrPriorityOutOfRange, priority, policy.MinPriority, policy.MaxPriority)
	default:
		return nil
	}
}

// AllowProperties is a policy construction helper: it returns a property
// set from the given identifiers, for use in Policy.Objects.
func AllowProperties(properties ...uint32) map[uint32]struct{} {
	set := make(map[uint32]struct{}, len(properties))
	for _, property := range properties {
		set[property] = struct{}{}
	}
	return set
}
