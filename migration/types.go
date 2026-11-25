package migration

import "math"

// Step is an authenticator counter value.
type Step uint64

// Interval is a closed step interval.
type Interval struct {
	Start Step `json:"start"`
	End   Step `json:"end"`
}

const FullDomainEnd Step = Step(math.MaxUint64)

// Valid reports whether the closed interval is well-formed.
func (i Interval) Valid() bool {
	return i.Start <= i.End
}

// Contains reports whether step belongs to the closed interval.
func (i Interval) Contains(step Step) bool {
	return i.Start <= step && step <= i.End
}

type Orientation string

const (
	Forward Orientation = "forward"
	Reverse Orientation = "reverse"
)

type DeviceSide string

const (
	OldSide DeviceSide = "old"
	NewSide DeviceSide = "new"
)

// Signature binds canonical evidence payload to a signer key.
type Signature struct {
	KeyID string `json:"keyId"`
	Value []byte `json:"value"`
}

// Segment is a signed old-to-new counter mapping segment.
type Segment struct {
	ID          string      `json:"id"`
	OldDeviceID string      `json:"oldDeviceId"`
	NewDeviceID string      `json:"newDeviceId"`
	Old         Interval    `json:"old"`
	New         Interval    `json:"new"`
	Orientation Orientation `json:"orientation"`
	Signature   Signature   `json:"signature"`
}

// Receipt is a signed record saying that RequestID consumed one step in Steps.
type Receipt struct {
	RequestID string     `json:"requestId"`
	Side      DeviceSide `json:"side"`
	DeviceID  string     `json:"deviceId"`
	Steps     Interval   `json:"steps"`
	Signature Signature  `json:"signature"`
}

// Input contains all migration evidence.
type Input struct {
	NewDeviceID       string    `json:"newDeviceId"`
	Domain            Interval  `json:"domain"`
	Segments          []Segment `json:"segments"`
	Receipts          []Receipt `json:"receipts"`
	RevokedSegmentIDs []string  `json:"revokedSegmentIds"`
}

type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Blocking Severity = "blocking"
)

// Finding describes a validation or compatibility result.
type Finding struct {
	Code      string    `json:"code,omitempty"`
	Severity  Severity  `json:"severity,omitempty"`
	Message   string    `json:"message,omitempty"`
	SegmentID string    `json:"segmentId,omitempty"`
	RequestID string    `json:"requestId,omitempty"`
	Interval  *Interval `json:"interval,omitempty"`
}

type Status string

const (
	StatusValid   Status = "valid"
	StatusInvalid Status = "invalid"
)

// RangeSet is a normalized union of disjoint closed intervals.
type RangeSet struct {
	Intervals []Interval `json:"intervals"`
}

// MappingClaim is a usable, conflict-free piece of a signed segment.
type MappingClaim struct {
	SegmentID   string      `json:"segmentId"`
	OldDeviceID string      `json:"oldDeviceId"`
	Old         Interval    `json:"old"`
	New         Interval    `json:"new"`
	Orientation Orientation `json:"orientation"`
}

// RequestClaim explains how one request ID can occupy a new-device step.
type RequestClaim struct {
	RequestID    string   `json:"requestId"`
	OldDeviceID  string   `json:"oldDeviceId"`
	ReceiptCount int      `json:"receiptCount"`
	OldReceipt   RangeSet `json:"oldReceipt"`
	NewReceipt   RangeSet `json:"newReceipt"`
	MappedNew    RangeSet `json:"mappedNew"`
	PossibleNew  RangeSet `json:"possibleNew"`
}

// Certificate is an interval decomposition that can be verified from Input.
type Certificate struct {
	NewDeviceID        string         `json:"newDeviceId"`
	Domain             Interval       `json:"domain"`
	Status             Status         `json:"status"`
	Findings           []Finding      `json:"findings"`
	RevokedSegmentIDs  []string       `json:"revokedSegmentIds"`
	RejectedSegmentIDs []string       `json:"rejectedSegmentIds"`
	Mapping            []MappingClaim `json:"mapping"`
	Coverage           RangeSet       `json:"coverage"`
	Conflicts          RangeSet       `json:"conflicts"`
	Blocked            RangeSet       `json:"blocked"`
	Requests           []RequestClaim `json:"requests"`
	Consumed           RangeSet       `json:"consumed"`
	Unknown            RangeSet       `json:"unknown"`
	Safe               RangeSet       `json:"safe"`
	Input              Input          `json:"input"`
}
