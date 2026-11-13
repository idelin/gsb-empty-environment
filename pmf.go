// Package netsec provides management-frame protection decisions for an
// 802.11 access point.
package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultSAQueryTimeout is used when Station.Timeout is not set.
const DefaultSAQueryTimeout = 5 * time.Second

// StatusAssociationTryAgainLater is the 802.11 status code returned when an
// already-associated station sends another association request.
const StatusAssociationTryAgainLater uint16 = 30

// MaxPacketNumber bounds a 48-bit 802.11 packet number (PN).
const MaxPacketNumber uint64 = 1<<48 - 1

// FrameType identifies the management frames for which this package makes a
// security decision.
type FrameType uint8

const (
	FrameTypeOther FrameType = iota
	FrameTypeDeauthentication
	FrameTypeDisassociation
	FrameTypeAssociationRequest
	FrameTypeReassociationRequest
	FrameTypeSAQueryResponse
)

// Short and descriptive aliases for the frame types.
const (
	FrameTypeDeauth      = FrameTypeDeauthentication
	FrameTypeDisassoc    = FrameTypeDisassociation
	FrameTypeAssoc       = FrameTypeAssociationRequest
	FrameTypeReassoc     = FrameTypeReassociationRequest
	FrameDeauth          = FrameTypeDeauthentication
	FrameDisassoc        = FrameTypeDisassociation
	FrameAssocRequest    = FrameTypeAssociationRequest
	FrameReassocRequest  = FrameTypeReassociationRequest
	FrameSAQueryResponse = FrameTypeSAQueryResponse
	Deauth               = FrameTypeDeauthentication
	Disassoc             = FrameTypeDisassociation
	AssocRequest         = FrameTypeAssociationRequest
	ReassocRequest       = FrameTypeReassociationRequest
	SAQueryResponse      = FrameTypeSAQueryResponse
)

// Destination classifies the receiver address of a management frame.
type Destination uint8

const (
	DestUnicast Destination = iota
	DestBroadcast
	DestMulticast
)

// Aliases for the destination classes.
const (
	DestinationUnicast   = DestUnicast
	DestinationBroadcast = DestBroadcast
	DestinationMulticast = DestMulticast
	Unicast              = DestUnicast
	Broadcast            = DestBroadcast
	Multicast            = DestMulticast
)

// Frame contains the security-relevant properties extracted by the AP's
// management-frame parser.
//
// Protected must be true only when the frame's robust management protection
// (for example, a valid CCMP-protected frame with its MMIE) has already been
// checked. BIPValid must be true only when the BIP integrity check has already
// succeeded with the station's IGTK. This package makes policy decisions; it
// deliberately does not treat an unverified "protected" bit as authentic.
type Frame struct {
	Type        FrameType
	Destination Destination

	// Broadcast and Group are convenience flags for parsers that do not use
	// Destination. Either flag makes the frame group-addressed.
	Broadcast bool
	Group     bool

	// Protected reports a verified individually protected robust management
	// frame. PN is its packet number.
	Protected bool
	PN        uint64

	// BIPValid reports a verified BIP integrity check. IPN is the integrity
	// packet number carried by the protected group management frame.
	BIPValid bool
	IPN      uint64

	// TransactionID is used by SA Query Response frames.
	TransactionID uint16
}

// ManagementFrame is an alias retained for callers that prefer a more explicit
// name.
type ManagementFrame = Frame

// GroupAddressed reports whether the frame is sent to more than one station.
func (f Frame) GroupAddressed() bool {
	return f.Broadcast || f.Group || f.Destination != DestUnicast
}

// Action is the operation the management-frame handler should perform.
type Action uint8

const (
	ActionIgnore Action = iota
	ActionAccept
	ActionDiscard
	ActionRejectTryAgain
	ActionRejectTryAgainAndSAQuery
	ActionKeepAssociation
	ActionTerminateAssociation
)

// Aliases useful to callers that name decisions rather than actions.
const (
	DecisionIgnore                = ActionIgnore
	DecisionAccept                = ActionAccept
	DecisionDiscard               = ActionDiscard
	DecisionRejectTryAgain        = ActionRejectTryAgain
	DecisionRejectTryAgainSAQuery = ActionRejectTryAgainAndSAQuery
	DecisionKeepAssociation       = ActionKeepAssociation
	DecisionTerminateAssociation  = ActionTerminateAssociation
)

// Reason explains why an action was selected.
type Reason uint8

const (
	ReasonNone Reason = iota
	ReasonUnprotectedManagement
	ReasonPNReplay
	ReasonInvalidPacketNumber
	ReasonBIPIntegrityFailure
	ReasonIPNReplay
	ReasonNotAssociated
	ReasonSAQueryNotPending
	ReasonSAQueryUnprotected
	ReasonSAQueryTransactionMismatch
	ReasonSAQueryTimeout
	ReasonDuplicateAssociation
)

// Decision is returned for every frame. StatusCode is populated when the AP
// must send an Association Response. SendSAQuery and TransactionID describe
// the SA Query Request that must be sent.
type Decision struct {
	Action        Action
	Reason        Reason
	StatusCode    uint16
	SendSAQuery   bool
	TransactionID uint16
}

// Dropped reports whether the frame must not be processed.
func (d Decision) Dropped() bool {
	return d.Action == ActionDiscard
}

// AssociationKept reports whether the existing association must survive.
func (d Decision) AssociationKept() bool {
	return d.Action == ActionKeepAssociation
}

// NeedsSAQuery reports whether the AP must send an SA Query Request.
func (d Decision) NeedsSAQuery() bool {
	return d.SendSAQuery
}

// SAQuery records an outstanding SA Query transaction.
type SAQuery struct {
	TransactionID uint16
	Started       time.Time
	Deadline      time.Time
}

// SAQueryState is an alias for SAQuery.
type SAQueryState = SAQuery

// Station is the per-STA state needed for PMF decisions. Its methods are safe
// for concurrent management-frame processing; do not copy a Station after it
// has been used.
type Station struct {
	// PMF is true when the station negotiated management-frame protection.
	PMF bool

	// PMFNegotiated is an alternate spelling accepted for integration
	// compatibility. Either this field or PMF enables PMF checks.
	PMFNegotiated bool

	Associated bool

	// LastPN is the highest authenticated individually protected packet
	// number accepted from this station.
	LastPN uint64

	// LastIPN is the highest authenticated BIP integrity packet number
	// accepted from this station.
	LastIPN uint64

	// SAQuery is non-nil while an SA Query transaction is outstanding.
	SAQuery *SAQuery

	// Timeout is the maximum time allowed for an SA Query Response.
	// DefaultSAQueryTimeout is used when this is zero.
	Timeout time.Duration

	// TransactionIDSource can be replaced by tests or by a deployment that
	// wants deterministic transaction identifiers. It must return values
	// without blocking.
	TransactionIDSource func() uint16

	mu sync.Mutex
}

// NewStation creates per-STA state. The optional associated argument sets the
// initial association state.
func NewStation(pmf bool, associated ...bool) *Station {
	s := &Station{
		PMF:           pmf,
		PMFNegotiated: pmf,
		Timeout:       DefaultSAQueryTimeout,
	}
	if len(associated) > 0 {
		s.Associated = associated[0]
	}
	return s
}

// StationState and STAState are aliases for Station.
type StationState = Station

// STAState is an alias for Station.
type STAState = Station

// SetAssociation updates the station's association state.
func (s *Station) SetAssociation(associated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Associated = associated
	if !associated {
		s.SAQuery = nil
	}
}

// Association returns the current association state.
func (s *Station) Association() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Associated
}

// PendingSAQuery returns a snapshot of the outstanding SA Query, if any.
func (s *Station) PendingSAQuery() *SAQuery {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SAQuery == nil {
		return nil
	}
	q := *s.SAQuery
	return &q
}

// Evaluate applies all PMF checks to one parsed management frame.
func (s *Station) Evaluate(f Frame, now time.Time) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()

	if d, expired := s.expireLocked(now); expired {
		return d
	}

	if f.GroupAddressed() {
		if !f.BIPValid {
			return Decision{Action: ActionDiscard, Reason: ReasonBIPIntegrityFailure}
		}
		if f.IPN == 0 || f.IPN > MaxPacketNumber || f.IPN <= s.LastIPN {
			return Decision{Action: ActionDiscard, Reason: ReasonIPNReplay}
		}
		s.LastIPN = f.IPN
		return Decision{Action: ActionAccept, Reason: ReasonNone}
	}

	if f.Protected {
		if f.PN == 0 || f.PN > MaxPacketNumber {
			return Decision{Action: ActionDiscard, Reason: ReasonInvalidPacketNumber}
		}
		if f.PN <= s.LastPN {
			return Decision{Action: ActionDiscard, Reason: ReasonPNReplay}
		}
		// A valid protected frame advances the replay window even when the
		// frame itself is rejected for a state-machine reason below.
		s.LastPN = f.PN
	}

	switch f.Type {
	case FrameTypeDeauthentication, FrameTypeDisassociation:
		if s.pmfEnabled() && !f.Protected {
			return Decision{Action: ActionDiscard, Reason: ReasonUnprotectedManagement}
		}
		s.Associated = false
		s.SAQuery = nil
		return Decision{Action: ActionAccept, Reason: ReasonNone}

	case FrameTypeAssociationRequest, FrameTypeReassociationRequest:
		if !s.Associated {
			return Decision{Action: ActionAccept, Reason: ReasonNone}
		}
		if !s.pmfEnabled() {
			return Decision{
				Action:     ActionRejectTryAgain,
				Reason:     ReasonDuplicateAssociation,
				StatusCode: StatusAssociationTryAgainLater,
			}
		}
		if s.SAQuery != nil {
			q := *s.SAQuery
			return Decision{
				Action:        ActionRejectTryAgainAndSAQuery,
				Reason:        ReasonDuplicateAssociation,
				StatusCode:    StatusAssociationTryAgainLater,
				SendSAQuery:   true,
				TransactionID: q.TransactionID,
			}
		}
		q := s.newSAQueryLocked(now)
		return Decision{
			Action:        ActionRejectTryAgainAndSAQuery,
			Reason:        ReasonDuplicateAssociation,
			StatusCode:    StatusAssociationTryAgainLater,
			SendSAQuery:   true,
			TransactionID: q.TransactionID,
		}

	case FrameTypeSAQueryResponse:
		if !f.Protected {
			return Decision{Action: ActionDiscard, Reason: ReasonSAQueryUnprotected}
		}
		if s.SAQuery == nil {
			return Decision{Action: ActionDiscard, Reason: ReasonSAQueryNotPending}
		}
		if f.TransactionID != s.SAQuery.TransactionID {
			return Decision{Action: ActionDiscard, Reason: ReasonSAQueryTransactionMismatch}
		}
		s.SAQuery = nil
		s.Associated = true
		return Decision{Action: ActionKeepAssociation, Reason: ReasonNone}

	default:
		return Decision{Action: ActionAccept, Reason: ReasonNone}
	}
}

// Handle is an alias for Evaluate.
func (s *Station) Handle(f Frame, now time.Time) Decision {
	return s.Evaluate(f, now)
}

// HandleFrame evaluates a frame using the current wall clock.
func (s *Station) HandleFrame(f Frame) Decision {
	return s.Evaluate(f, time.Now())
}

// EvaluateStation is a package-level convenience function.
func EvaluateStation(s *Station, f Frame, now time.Time) Decision {
	return s.Evaluate(f, now)
}

// Expire checks the SA Query deadline. It returns ActionTerminateAssociation
// when the response window has elapsed and the station must be disassociated.
func (s *Station) Expire(now time.Time) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, expired := s.expireLocked(now)
	if !expired {
		return Decision{Action: ActionIgnore, Reason: ReasonNone}
	}
	return d
}

// Tick is an alias for Expire.
func (s *Station) Tick(now time.Time) Decision {
	return s.Expire(now)
}

// StartSAQuery starts a deterministic SA Query transaction. It is intended for
// tests and for integrations that generate identifiers themselves.
func (s *Station) StartSAQuery(now time.Time, transactionID uint16) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.SAQuery = &SAQuery{
		TransactionID: transactionID,
		Started:       now,
		Deadline:      now.Add(s.timeoutLocked()),
	}
	return Decision{
		Action:        ActionRejectTryAgainAndSAQuery,
		Reason:        ReasonDuplicateAssociation,
		StatusCode:    StatusAssociationTryAgainLater,
		SendSAQuery:   true,
		TransactionID: transactionID,
	}
}

func (s *Station) pmfEnabled() bool {
	return s.PMF || s.PMFNegotiated
}

func (s *Station) timeoutLocked() time.Duration {
	if s.Timeout <= 0 {
		return DefaultSAQueryTimeout
	}
	return s.Timeout
}

func (s *Station) newSAQueryLocked(now time.Time) *SAQuery {
	id := s.nextTransactionID()
	q := &SAQuery{
		TransactionID: id,
		Started:       now,
		Deadline:      now.Add(s.timeoutLocked()),
	}
	s.SAQuery = q
	return q
}

func (s *Station) expireLocked(now time.Time) (Decision, bool) {
	if s.SAQuery == nil || now.Before(s.SAQuery.Deadline) {
		return Decision{}, false
	}
	s.SAQuery = nil
	s.Associated = false
	return Decision{
		Action: ActionTerminateAssociation,
		Reason: ReasonSAQueryTimeout,
	}, true
}

func (s *Station) nextTransactionID() uint16 {
	if s.TransactionIDSource != nil {
		return s.TransactionIDSource()
	}
	var b [2]byte
	if _, err := rand.Read(b[:]); err == nil {
		id := binary.BigEndian.Uint16(b[:])
		if id != 0 {
			return id
		}
	}
	for {
		id := uint16(atomic.AddUint32(&fallbackTransactionID, 1))
		if id != 0 {
			return id
		}
	}
}

var fallbackTransactionID uint32
