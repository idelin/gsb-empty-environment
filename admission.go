package netsec

import (
	"errors"
	"sync"
	"time"
)

const (
	// DefaultMaintenanceMaster is the source address permitted to issue cold
	// and hot restarts when no maintenance master list is configured.
	DefaultMaintenanceMaster uint16 = 0

	// DefaultSelectTimeout is the maximum select-to-operate lifetime when the
	// policy does not specify one.
	DefaultSelectTimeout = 5 * time.Second
)

// Request is one DNP3 application request received by the gateway.
//
// Source is the master address and Destination is the outstation address.
// Fragments belong to exactly one transport message; an incomplete message is
// rejected rather than buffered.
//
// Timeout optionally shortens the policy select-to-operate lifetime for a
// SELECT. Deadline optionally supplies an absolute caller deadline. Both are
// bounded by Policy.SelectTimeout.
type Request struct {
	Source      uint16
	Destination uint16
	Fragments   []TransportFragment
	Timeout     time.Duration
	Deadline    time.Time
}

// Policy configures restart and control-operation admission.
//
// Cold and hot restarts have independent authorizations. If a dedicated list
// is non-empty, only the addresses in that list can send the corresponding
// restart. If it is empty, the maintenance list applies. If the maintenance
// list is also empty, DefaultMaintenanceMaster is used. The default is therefore
// fail-safe: ordinary masters cannot issue restarts.
type Policy struct {
	// MaintenanceMasters contains the master/source addresses allowed to send
	// both restart types when no dedicated restart list is configured.
	MaintenanceMasters []uint16

	// ColdRestartMasters authorizes cold restart (FC 13). When non-empty it
	// replaces MaintenanceMasters for cold restart only.
	ColdRestartMasters []uint16

	// HotRestartMasters authorizes hot restart (FC 14). When non-empty it
	// replaces MaintenanceMasters for hot restart only.
	HotRestartMasters []uint16

	// AllowedFunctionCodes optionally restricts the known function codes that
	// may be admitted. A nil list allows all known codes, subject to the
	// restart and select/operate rules. A non-nil empty list denies all
	// functions. Unknown codes are always rejected.
	AllowedFunctionCodes []FunctionCode

	// SelectTimeout is the default and maximum select-to-operate lifetime. A
	// caller-provided Request.Timeout can shorten it but cannot extend it.
	SelectTimeout time.Duration

	// MaxMessageBytes bounds reassembled transport payloads.
	MaxMessageBytes int

	// Clock supplies the current time. It is normally time.Now and exists so
	// callers can use an authoritative or test clock.
	Clock func() time.Time
}

// Reason explains an admission decision.
type Reason string

const (
	ReasonAllowed                   Reason = "allowed"
	ReasonIncompleteAssembly        Reason = "incomplete_transport_assembly"
	ReasonMalformedTransport        Reason = "malformed_transport"
	ReasonMalformedApplication      Reason = "malformed_application"
	ReasonUnknownFunction           Reason = "unknown_function"
	ReasonFunctionNotAllowed        Reason = "function_not_allowed"
	ReasonRestartNotAuthorized      Reason = "restart_not_authorized"
	ReasonSelectRequired            Reason = "select_required"
	ReasonSelectExpired             Reason = "select_expired"
	ReasonSelectMasterMismatch      Reason = "select_master_mismatch"
	ReasonSelectDestinationMismatch Reason = "select_destination_mismatch"
	ReasonSelectSequenceMismatch    Reason = "select_sequence_mismatch"
)

// Decision is the result of an admission check.
type Decision struct {
	Allowed     bool
	Reason      Reason
	Function    FunctionCode
	Sequence    uint8
	Source      uint16
	Destination uint16
}

type selectKey struct {
	source      uint16
	destination uint16
	sequence    uint8
}

type pendingSelect struct {
	expires time.Time
}

type policySet map[uint16]struct{}

// Gateway is a stateful admission controller. It is safe for concurrent use.
// A new Gateway should be created for each independent enforcement domain
// (for example, each protected outstation).
type Gateway struct {
	maintenance policySet
	cold        policySet
	hot         policySet
	allowed     map[FunctionCode]struct{}
	selectTTL   time.Duration
	maxBytes    int
	clock       func() time.Time

	mu      sync.Mutex
	pending map[selectKey]pendingSelect
}

// New creates an admission gateway from policy. The zero policy admits all
// known function codes, but only DefaultMaintenanceMaster may send restarts.
func New(policy Policy) *Gateway {
	if policy.SelectTimeout <= 0 {
		policy.SelectTimeout = DefaultSelectTimeout
	}
	if policy.MaxMessageBytes <= 0 {
		policy.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if policy.Clock == nil {
		policy.Clock = time.Now
	}

	maintenance := makeAddressSet(policy.MaintenanceMasters)
	if len(maintenance) == 0 {
		maintenance = policySet{DefaultMaintenanceMaster: {}}
	}

	var allowed map[FunctionCode]struct{}
	if policy.AllowedFunctionCodes != nil {
		allowed = make(map[FunctionCode]struct{}, len(policy.AllowedFunctionCodes))
		for _, function := range policy.AllowedFunctionCodes {
			allowed[function] = struct{}{}
		}
	}

	return &Gateway{
		maintenance: maintenance,
		cold:        makeAddressSet(policy.ColdRestartMasters),
		hot:         makeAddressSet(policy.HotRestartMasters),
		allowed:     allowed,
		selectTTL:   policy.SelectTimeout,
		maxBytes:    policy.MaxMessageBytes,
		clock:       policy.Clock,
		pending:     make(map[selectKey]pendingSelect),
	}
}

// NewGateway is an alias for New.
func NewGateway(policy Policy) *Gateway {
	return New(policy)
}

func makeAddressSet(addresses []uint16) policySet {
	if len(addresses) == 0 {
		return nil
	}
	set := make(policySet, len(addresses))
	for _, address := range addresses {
		set[address] = struct{}{}
	}
	return set
}

func contains(set policySet, address uint16) bool {
	_, ok := set[address]
	return ok
}

func (g *Gateway) restartAuthorized(function FunctionCode, source uint16) bool {
	switch function {
	case FCColdRestart:
		if len(g.cold) > 0 {
			return contains(g.cold, source)
		}
		return contains(g.maintenance, source)
	case FCHotRestart:
		if len(g.hot) > 0 {
			return contains(g.hot, source)
		}
		return contains(g.maintenance, source)
	default:
		return true
	}
}

func (g *Gateway) functionAllowed(function FunctionCode) bool {
	if g.allowed == nil {
		return true
	}
	_, ok := g.allowed[function]
	return ok
}

// Admit reassembles the transport fragments, parses the application function
// code, and applies policy. It must be called before forwarding a request.
//
// SELECT is recorded when admitted; OPERATE is admitted only if a matching
// SELECT from the same source, to the same destination, with the same
// application sequence number is still pending and has not timed out. A
// successful OPERATE consumes the SELECT, so replaying it fails.
func (g *Gateway) Admit(request Request) Decision {
	decision := Decision{
		Source:      request.Source,
		Destination: request.Destination,
		Reason:      ReasonMalformedTransport,
	}

	payload, err := ReassembleTransport(request.Fragments, g.maxBytes)
	if err != nil {
		if errors.Is(err, ErrIncompleteTransport) {
			decision.Reason = ReasonIncompleteAssembly
		} else {
			decision.Reason = ReasonMalformedTransport
		}
		return decision
	}

	application, err := ParseApplicationRequest(payload)
	if err != nil {
		decision.Reason = ReasonMalformedApplication
		return decision
	}
	decision.Function = application.Function
	decision.Sequence = application.Sequence

	if !application.Function.Known() {
		decision.Reason = ReasonUnknownFunction
		return decision
	}
	if !g.functionAllowed(application.Function) {
		decision.Reason = ReasonFunctionNotAllowed
		return decision
	}

	if application.Function == FCColdRestart || application.Function == FCHotRestart {
		if !g.restartAuthorized(application.Function, request.Source) {
			decision.Reason = ReasonRestartNotAuthorized
			return decision
		}
		decision.Allowed = true
		decision.Reason = ReasonAllowed
		return decision
	}

	now := g.clock()

	switch application.Function {
	case FCSelect:
		if !request.Deadline.IsZero() && !now.Before(request.Deadline) {
			decision.Reason = ReasonSelectExpired
			return decision
		}
		deadline := g.selectDeadline(request, now)
		if !now.Before(deadline) {
			decision.Reason = ReasonSelectExpired
			return decision
		}

		key := selectKey{
			source:      request.Source,
			destination: request.Destination,
			sequence:    application.Sequence,
		}
		g.mu.Lock()
		g.removeExpired(now)
		g.pending[key] = pendingSelect{expires: deadline}
		g.mu.Unlock()

		decision.Allowed = true
		decision.Reason = ReasonAllowed
		return decision

	case FCOperate:
		if !request.Deadline.IsZero() && !now.Before(request.Deadline) {
			decision.Reason = ReasonSelectExpired
			return decision
		}
		key := selectKey{
			source:      request.Source,
			destination: request.Destination,
			sequence:    application.Sequence,
		}

		g.mu.Lock()
		entry, ok := g.pending[key]
		if ok {
			if !now.Before(entry.expires) {
				delete(g.pending, key)
				g.mu.Unlock()
				decision.Reason = ReasonSelectExpired
				return decision
			}
			delete(g.pending, key)
			g.removeExpired(now)
			g.mu.Unlock()
			decision.Allowed = true
			decision.Reason = ReasonAllowed
			return decision
		}
		decision.Reason = g.missingSelectReason(key)
		g.removeExpired(now)
		g.mu.Unlock()
		return decision
	}

	decision.Allowed = true
	decision.Reason = ReasonAllowed
	return decision
}

// Check is an alias for Admit.
func (g *Gateway) Check(request Request) Decision {
	return g.Admit(request)
}

func (g *Gateway) selectDeadline(request Request, now time.Time) time.Time {
	lifetime := g.selectTTL
	if request.Timeout > 0 && request.Timeout < lifetime {
		lifetime = request.Timeout
	}
	deadline := now.Add(lifetime)
	if !request.Deadline.IsZero() && request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	return deadline
}

func (g *Gateway) removeExpired(now time.Time) {
	for key, entry := range g.pending {
		if !now.Before(entry.expires) {
			delete(g.pending, key)
		}
	}
}

// missingSelectReason returns a diagnostic reason for a missing SELECT match.
// The caller must hold g.mu.
func (g *Gateway) missingSelectReason(want selectKey) Reason {
	for key := range g.pending {
		switch {
		case key.destination == want.destination &&
			key.sequence == want.sequence &&
			key.source != want.source:
			return ReasonSelectMasterMismatch
		case key.source == want.source &&
			key.destination == want.destination &&
			key.sequence != want.sequence:
			return ReasonSelectSequenceMismatch
		case key.source == want.source &&
			key.sequence == want.sequence &&
			key.destination != want.destination:
			return ReasonSelectDestinationMismatch
		}
	}
	return ReasonSelectRequired
}
