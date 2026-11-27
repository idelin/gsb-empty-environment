// Package dra provides a self-contained, importable implementation of the
// Kubernetes Dynamic Resource Allocation (DRA) structured-device model:
// ResourceSlice/DeviceClass/ResourceClaim matching, backtracking allocation
// with MatchAttribute constraints, ResourceSlice generation completeness
// checks and claim reservation transaction management.
package dra

import "errors"

// AllocationMode mirrors resourceapi.AllocationMode.
type AllocationMode string

const (
	// AllocationModeAll allocates every device of a pool that satisfies the
	// request selectors.
	AllocationModeAll AllocationMode = "All"
	// AllocationModeExactCount allocates exactly Count satisfying devices.
	AllocationModeExactCount AllocationMode = "ExactCount"
)

// AttributeKind enumerates the supported device attribute value types.
type AttributeKind int

const (
	KindBool AttributeKind = iota
	KindInt64
	KindString
	KindVersion
)

// AttributeValue is a tagged union mirroring resourceapi.DeviceAttribute.
type AttributeValue struct {
	kind    AttributeKind
	boolV   bool
	intV    int64
	stringV string
}

// BoolValue builds a boolean attribute/requirement value.
func BoolValue(v bool) AttributeValue {
	return AttributeValue{kind: KindBool, boolV: v}
}

// Int64Value builds an integer attribute/requirement value.
func Int64Value(v int64) AttributeValue {
	return AttributeValue{kind: KindInt64, intV: v}
}

// StringValue builds a string attribute/requirement value.
func StringValue(v string) AttributeValue {
	return AttributeValue{kind: KindString, stringV: v}
}

// VersionValue builds a semantic-version attribute/requirement value.
func VersionValue(v string) AttributeValue {
	return AttributeValue{kind: KindVersion, stringV: v}
}

// Kind returns the value's attribute kind.
func (a AttributeValue) Kind() AttributeKind { return a.kind }

// Bool returns the boolean value.
func (a AttributeValue) Bool() bool { return a.boolV }

// Int64 returns the integer value.
func (a AttributeValue) Int64() int64 { return a.intV }

// String returns the string/version value.
func (a AttributeValue) String() string { return a.stringV }

// MatchOp enumerates the comparison operators usable in a selector.
type MatchOp string

const (
	// OpEqual is equality for string/int/bool/semver attributes.
	OpEqual MatchOp = "="
	// OpNotEqual is the inverse of OpEqual.
	OpNotEqual MatchOp = "!="
	// OpLessThan / OpLessEqual / OpGreaterThan / OpGreaterEqual are ordered
	// comparisons (integers or semantic versions).
	OpLessThan     MatchOp = "<"
	OpLessEqual    MatchOp = "<="
	OpGreaterThan  MatchOp = ">"
	OpGreaterEqual MatchOp = ">="
	// OpIn / OpNotIn test string membership.
	OpIn    MatchOp = "In"
	OpNotIn MatchOp = "NotIn"
	// OpSemverCompatible implements the npm "caret" semantics: the device
	// version is in [R, next-incompatible).
	OpSemverCompatible MatchOp = "SemverCompatible"
	// OpCapacityGte is a capacity lower-bound check.
	OpCapacityGte MatchOp = "GtEq"
)

// AttributeRequirement is one predicate against a named device attribute.
type AttributeRequirement struct {
	Attribute string
	Op        MatchOp
	Value     AttributeValue
}

// CapacityRequirement is one predicate against a named capacity entry.
type CapacityRequirement struct {
	Name  string
	Op    MatchOp
	Value int64
}

// Selector mirrors resourceapi.DeviceSelector: every requirement must match
// (logical AND); multiple selectors combine with AND as well.
type Selector struct {
	AttributeRequirements []AttributeRequirement
	CapacityRequirements  []CapacityRequirement
}

// DeviceClass mirrors resourceapi.DeviceClass.
type DeviceClass struct {
	Name      string
	Selectors []Selector
}

// DeviceRequest mirrors resourceapi.DeviceRequest.
type DeviceRequest struct {
	Name           string
	ClassName      string
	AllocationMode AllocationMode
	Count          int64
	Selectors      []Selector
}

// MatchAttributeConstraint requires that the devices selected for every named
// request expose the same value for Attribute.
type MatchAttributeConstraint struct {
	Attribute string
	Requests  []string
}

// ClaimConstraint wraps the claim-level constraints.
type ClaimConstraint struct {
	MatchAttribute *MatchAttributeConstraint
}

// Device mirrors resourceapi.Device.
type Device struct {
	Name       string
	Attributes map[string]AttributeValue
	Capacities map[string]int64
}

// ResourcePool mirrors resourceapi.ResourcePool.
type ResourcePool struct {
	Name               string
	Generation         int64
	ResourceSliceCount int64
}

// ResourceSlice mirrors resourceapi.ResourceSlice as published by a driver.
type ResourceSlice struct {
	Name     string
	NodeName string
	Driver   string
	Pool     ResourcePool
	Devices  []Device
}

// ResourceClaim mirrors resourceapi.ResourceClaim (spec plus cached status).
type ResourceClaim struct {
	Namespace string
	Name      string
	// Shared allows more than one consumer to reserve the claim; a
	// non-shared claim accepts at most one ReservedFor entry.
	Shared      bool
	Requests    []DeviceRequest
	Constraints []ClaimConstraint

	// Status fields: once allocated, Allocation is non-nil.
	Allocation  *AllocationResult
	ReservedFor []Consumer
}

// Consumer identifies a user of a claim, typically a pod.
type Consumer struct {
	UID       string
	Namespace string
	Name      string
}

// Key renders the stable map key for a consumer.
func (c Consumer) Key() string {
	return c.UID + "|" + c.Namespace + "/" + c.Name
}

// DeviceResult is one allocated device reference.
type DeviceResult struct {
	Driver string
	Pool   string
	Device string
}

// Key returns the globally unique (Driver, Pool, Device) identity.
func (d DeviceResult) Key() string {
	return d.Driver + "/" + d.Pool + "/" + d.Device
}

// RequestResult captures the devices allocated to one named DeviceRequest.
type RequestResult struct {
	Request string
	Devices []DeviceResult
}

// AllocationResult is the solver output stored on an allocated claim.
type AllocationResult struct {
	Node    string
	Results []RequestResult
}

// ClaimKey returns the stable map key for a namespaced claim.
func ClaimKey(namespace, name string) string {
	return namespace + "/" + name
}

// Key is a convenience accessor on ResourceClaim.
func (c *ResourceClaim) Key() string {
	return ClaimKey(c.Namespace, c.Name)
}

var (
	// ErrIncompletePool means the node's pool view lacks slices for its
	// current generation and must be ignored.
	ErrIncompletePool = errors.New("dra: resource pool generation is incomplete")
	// ErrNoDevices means no device combination satisfies the claim.
	ErrNoDevices = errors.New("dra: no devices available satisfying the claim")
	// ErrAlreadyReserved is returned when an exclusive claim is reserved by
	// another consumer.
	ErrAlreadyReserved = errors.New("dra: claim is already reserved exclusively")
	// ErrNotFound is returned for unknown classes or claims.
	ErrNotFound = errors.New("dra: object not found")
	// ErrInvalidSpec is returned for malformed requests/selectors.
	ErrInvalidSpec = errors.New("dra: invalid resource claim specification")
	// ErrNotReserved is returned when unreserving an unknown consumer.
	ErrNotReserved = errors.New("dra: claim has no such reservation")
	// ErrNodeMismatch means an allocated claim lives on another node.
	ErrNodeMismatch = errors.New("dra: allocated claim is bound to a different node")
)
