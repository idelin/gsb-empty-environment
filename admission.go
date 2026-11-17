package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	// MBAPHeaderSize is the size of the Modbus/TCP MBAP header.
	MBAPHeaderSize = 7

	// MaxPDUSize is the largest Modbus PDU carried by a Modbus/TCP ADU.
	MaxPDUSize = 253

	// MaxADUSize is the largest complete Modbus/TCP ADU.
	MaxADUSize = 260

	maxMBAPLength = 1 + MaxPDUSize
)

var (
	ErrShortFrame               = errors.New("modbus: frame shorter than MBAP header")
	ErrProtocolID               = errors.New("modbus: MBAP protocol identifier is not zero")
	ErrLengthMismatch           = errors.New("modbus: MBAP length does not match frame bytes")
	ErrOversizedFrame           = errors.New("modbus: frame exceeds Modbus/TCP maximum")
	ErrMissingPDU               = errors.New("modbus: PDU has no function code")
	ErrInvalidFunctionCode      = errors.New("modbus: invalid request function code")
	ErrUnitDenied               = errors.New("modbus: unit ID is not allowed by zone policy")
	ErrFunctionDenied           = errors.New("modbus: function code is not allowed by zone policy")
	ErrWriteDenied              = errors.New("modbus: write function code is disabled for zone")
	ErrTransactionConflict      = errors.New("modbus: transaction ID already has a pending request")
	ErrPendingLimit             = errors.New("modbus: too many pending requests")
	ErrNoPendingTransaction     = errors.New("modbus: no pending request for transaction")
	ErrResponseMismatch         = errors.New("modbus: response does not match any pending request")
	ErrAmbiguousResponse        = errors.New("modbus: response matches more than one pending request")
	ErrResponseUnitMismatch     = errors.New("modbus: response unit ID does not match request")
	ErrResponseFunctionMismatch = errors.New("modbus: response function code does not match request")
	ErrMalformedException       = errors.New("modbus: malformed exception response")
)

// UnitPolicy describes which function codes may target a single serial slave.
// Functions must be listed explicitly. Write function codes are denied unless
// AllowWrites is true and the function code is also listed in Functions.
type UnitPolicy struct {
	Functions   []uint8
	AllowWrites bool
}

// Policy is the caller-provided zone policy. Unit IDs that are absent from Zones
// are denied. A zero-value Policy denies every request.
type Policy struct {
	Zones map[uint8]UnitPolicy
}

// Request is the security-relevant portion of an admitted Modbus/TCP request.
type Request struct {
	ClientID      string
	TransactionID uint16
	UnitID        uint8
	FunctionCode  uint8
}

// Response is the security-relevant portion of an admitted Modbus/TCP response.
// FunctionCode is the code as received, so an exception response contains the
// request function code with the high bit set. IsException reports that case.
type Response struct {
	ClientID      string
	TransactionID uint16
	UnitID        uint8
	FunctionCode  uint8
	Exception     bool
	ExceptionCode uint8
}

// IsException reports whether the response is a Modbus exception response.
func (r Response) IsException() bool {
	return r.Exception
}

// Option configures an Admission.
type Option func(*Admission)

// WithLogger sets the logger used for rejected requests and responses.
func WithLogger(logger *slog.Logger) Option {
	return func(a *Admission) {
		a.logger = logger
	}
}

// WithTimeout sets how long a request may remain pending. A response that
// arrives after the timeout is treated as having no matching request.
func WithTimeout(timeout time.Duration) Option {
	return func(a *Admission) {
		if timeout > 0 {
			a.timeout = timeout
		}
	}
}

// WithMaxPending bounds the number of outstanding requests held by one
// Admission instance.
func WithMaxPending(max int) Option {
	return func(a *Admission) {
		if max > 0 {
			a.maxPending = max
		}
	}
}

// WithClock replaces the clock used for pending-request expiry. It is primarily
// useful for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(a *Admission) {
		if now != nil {
			a.now = now
		}
	}
}

// Admission validates requests and binds responses to outstanding client
// transactions. It is safe for concurrent use by multiple forwarding loops.
type Admission struct {
	units      map[uint8]unitRule
	logger     *slog.Logger
	timeout    time.Duration
	maxPending int
	now        func() time.Time

	mu      sync.Mutex
	pending map[pendingKey]pendingRequest
}

type unitRule struct {
	functions   map[uint8]struct{}
	allowWrites bool
}

type pendingKey struct {
	clientID      string
	transactionID uint16
}

type pendingRequest struct {
	unitID       uint8
	functionCode uint8
	expiresAt    time.Time
}

type responseCandidate struct {
	key     pendingKey
	pending pendingRequest
}

// NewAdmission builds an admission engine from a zone policy. The policy is
// copied, so later mutations of the caller's maps and slices do not affect the
// engine.
func NewAdmission(policy Policy, opts ...Option) *Admission {
	a := &Admission{
		units:      compilePolicy(policy),
		logger:     slog.Default(),
		timeout:    30 * time.Second,
		maxPending: 4096,
		now:        time.Now,
		pending:    make(map[pendingKey]pendingRequest),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	if a.logger == nil {
		a.logger = slog.Default()
	}
	return a
}

// New is an alias for NewAdmission.
func New(policy Policy, opts ...Option) *Admission {
	return NewAdmission(policy, opts...)
}

// IsWriteFunction reports whether fc is one of the write-class function codes
// that are denied by default: 5, 6, 15, 16, and 23.
func IsWriteFunction(fc uint8) bool {
	switch fc {
	case 5, 6, 15, 16, 23:
		return true
	default:
		return false
	}
}

// AdmitRequest validates a complete Modbus/TCP ADU and, if accepted, records
// the request as pending for clientID. clientID must uniquely identify the
// client connection. Transaction IDs are scoped to clientID, so different
// clients may independently use the same transaction ID.
func (a *Admission) AdmitRequest(clientID string, frame []byte) (Request, error) {
	h, pdu, err := parseADU(frame)
	if err != nil {
		tx, unit, fc := peekFields(frame)
		a.logReject("modbus request rejected", clientID, tx, unit, fc, len(frame), err)
		return Request{}, err
	}

	req := Request{
		ClientID:      clientID,
		TransactionID: h.transactionID,
		UnitID:        h.unitID,
		FunctionCode:  pdu[0],
	}
	if req.FunctionCode == 0 || req.FunctionCode&0x80 != 0 {
		err := fmt.Errorf("%w: function=0x%02x", ErrInvalidFunctionCode, req.FunctionCode)
		a.logReject("modbus request rejected", clientID, req.TransactionID, req.UnitID, req.FunctionCode, len(frame), err)
		return Request{}, err
	}
	if err := a.checkPolicy(req.UnitID, req.FunctionCode); err != nil {
		a.logReject("modbus request rejected", clientID, req.TransactionID, req.UnitID, req.FunctionCode, len(frame), err)
		return Request{}, err
	}

	now := a.now()
	key := pendingKey{clientID: clientID, transactionID: req.TransactionID}

	a.mu.Lock()
	if pending, exists := a.pending[key]; exists {
		if now.Before(pending.expiresAt) {
			a.mu.Unlock()
			err := fmt.Errorf("%w: client=%q transaction=%d", ErrTransactionConflict, clientID, req.TransactionID)
			a.logReject("modbus request rejected", clientID, req.TransactionID, req.UnitID, req.FunctionCode, len(frame), err)
			return Request{}, err
		}
		delete(a.pending, key)
	}
	if len(a.pending) >= a.maxPending {
		a.dropExpiredLocked(now)
		if len(a.pending) >= a.maxPending {
			a.mu.Unlock()
			err := fmt.Errorf("%w: limit=%d", ErrPendingLimit, a.maxPending)
			a.logReject("modbus request rejected", clientID, req.TransactionID, req.UnitID, req.FunctionCode, len(frame), err)
			return Request{}, err
		}
	}
	a.pending[key] = pendingRequest{
		unitID:       req.UnitID,
		functionCode: req.FunctionCode,
		expiresAt:    now.Add(a.timeout),
	}
	a.mu.Unlock()

	return req, nil
}

// AdmitResponse validates a complete Modbus/TCP ADU against the pending request
// for the same client and transaction ID. A matching response consumes the
// pending entry, so duplicate or racing replies are rejected. A mismatched
// response is logged and discarded without consuming the legitimate pending
// request. Use MatchResponse instead when the forwarding loop does not already
// know which client owns a serial response.
func (a *Admission) AdmitResponse(clientID string, frame []byte) (Response, error) {
	h, pdu, err := parseADU(frame)
	if err != nil {
		tx, unit, fc := peekFields(frame)
		a.logReject("modbus response rejected", clientID, tx, unit, fc, len(frame), err)
		return Response{}, err
	}

	key := pendingKey{clientID: clientID, transactionID: h.transactionID}

	a.mu.Lock()
	pending, ok := a.pending[key]
	if ok && !a.now().Before(pending.expiresAt) {
		delete(a.pending, key)
		ok = false
	}
	if !ok {
		a.mu.Unlock()
		err := fmt.Errorf("%w: client=%q transaction=%d", ErrNoPendingTransaction, clientID, h.transactionID)
		a.logReject("modbus response rejected", clientID, h.transactionID, h.unitID, pdu[0], len(frame), err)
		return Response{}, err
	}
	resp, err := validateResponse(clientID, h, pdu, pending)
	if err != nil {
		a.mu.Unlock()
		a.logResponseReject(clientID, h, pdu[0], pending, len(frame), err)
		return Response{}, err
	}

	delete(a.pending, key)
	a.mu.Unlock()

	return resp, nil
}

// MatchResponse validates a response from a serial slave and resolves it to
// exactly one pending client transaction. It is intended for a central
// forwarding loop that does not already know which client owns the reply.
//
// A response is ambiguous when multiple clients have pending requests with the
// same transaction ID, unit ID, and function code. Ambiguous responses are
// rejected without consuming any pending request, because delivering to either
// client could leak another client's transaction.
func (a *Admission) MatchResponse(frame []byte) (Response, error) {
	h, pdu, err := parseADU(frame)
	if err != nil {
		tx, unit, fc := peekFields(frame)
		a.logReject("modbus response rejected", "", tx, unit, fc, len(frame), err)
		return Response{}, err
	}

	now := a.now()
	a.mu.Lock()

	var sameTransaction []responseCandidate
	for key, pending := range a.pending {
		if !now.Before(pending.expiresAt) {
			delete(a.pending, key)
			continue
		}
		if key.transactionID != h.transactionID {
			continue
		}
		sameTransaction = append(sameTransaction, responseCandidate{key: key, pending: pending})
	}

	switch len(sameTransaction) {
	case 0:
		a.mu.Unlock()
		err := fmt.Errorf("%w: transaction=%d", ErrNoPendingTransaction, h.transactionID)
		a.logReject("modbus response rejected", "", h.transactionID, h.unitID, pdu[0], len(frame), err)
		return Response{}, err

	case 1:
		matched := sameTransaction[0]
		resp, err := validateResponse(matched.key.clientID, h, pdu, matched.pending)
		if err != nil {
			a.mu.Unlock()
			a.logResponseReject(matched.key.clientID, h, pdu[0], matched.pending, len(frame), err)
			return Response{}, err
		}
		delete(a.pending, matched.key)
		a.mu.Unlock()
		return resp, nil
	}

	matches := sameTransaction[:0]
	for _, candidate := range sameTransaction {
		if candidate.pending.unitID == h.unitID && functionMatches(pdu[0], candidate.pending.functionCode) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		a.mu.Unlock()
		err := fmt.Errorf("%w: transaction=%d unit=%d function=0x%02x", ErrResponseMismatch, h.transactionID, h.unitID, pdu[0])
		a.logReject("modbus response rejected", "", h.transactionID, h.unitID, pdu[0], len(frame), err)
		return Response{}, err
	}
	if len(matches) > 1 {
		a.mu.Unlock()
		err := fmt.Errorf("%w: transaction=%d unit=%d function=0x%02x candidates=%d",
			ErrAmbiguousResponse, h.transactionID, h.unitID, pdu[0], len(matches))
		a.logReject("modbus response rejected", "", h.transactionID, h.unitID, pdu[0], len(frame), err)
		return Response{}, err
	}

	matched := matches[0]
	resp, err := validateResponse(matched.key.clientID, h, pdu, matched.pending)
	if err != nil {
		a.mu.Unlock()
		a.logResponseReject(matched.key.clientID, h, pdu[0], matched.pending, len(frame), err)
		return Response{}, err
	}

	delete(a.pending, matched.key)
	a.mu.Unlock()
	return resp, nil
}

// PendingCount returns the number of live pending requests.
func (a *Admission) PendingCount() int {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dropExpiredLocked(now)
	return len(a.pending)
}

func (a *Admission) checkPolicy(unitID, functionCode uint8) error {
	rule, ok := a.units[unitID]
	if !ok {
		return fmt.Errorf("%w: unit=%d", ErrUnitDenied, unitID)
	}
	if IsWriteFunction(functionCode) && !rule.allowWrites {
		return fmt.Errorf("%w: unit=%d function=%d", ErrWriteDenied, unitID, functionCode)
	}
	if _, ok := rule.functions[functionCode]; !ok {
		return fmt.Errorf("%w: unit=%d function=%d", ErrFunctionDenied, unitID, functionCode)
	}
	return nil
}

func validateResponse(clientID string, h mbapHeader, pdu []byte, pending pendingRequest) (Response, error) {
	if h.unitID != pending.unitID {
		return Response{}, fmt.Errorf("%w: got=%d want=%d transaction=%d", ErrResponseUnitMismatch, h.unitID, pending.unitID, h.transactionID)
	}

	resp := Response{
		ClientID:      clientID,
		TransactionID: h.transactionID,
		UnitID:        h.unitID,
		FunctionCode:  pdu[0],
	}
	switch {
	case pdu[0] == pending.functionCode:
		return resp, nil
	case pdu[0] == pending.functionCode|0x80:
		if len(pdu) != 2 {
			return Response{}, fmt.Errorf("%w: got_pdu_bytes=%d want=2 transaction=%d", ErrMalformedException, len(pdu), h.transactionID)
		}
		resp.Exception = true
		resp.ExceptionCode = pdu[1]
		return resp, nil
	default:
		return Response{}, fmt.Errorf("%w: got=0x%02x want=0x%02x or 0x%02x transaction=%d",
			ErrResponseFunctionMismatch, pdu[0], pending.functionCode, pending.functionCode|0x80, h.transactionID)
	}
}

func functionMatches(got, want uint8) bool {
	return got == want || got == want|0x80
}

func (a *Admission) dropExpiredLocked(now time.Time) {
	for key, pending := range a.pending {
		if !now.Before(pending.expiresAt) {
			delete(a.pending, key)
		}
	}
}

func (a *Admission) logResponseReject(clientID string, h mbapHeader, gotFC uint8, pending pendingRequest, frameLen int, err error) {
	a.logReject(
		"modbus response rejected",
		clientID,
		h.transactionID,
		h.unitID,
		gotFC,
		frameLen,
		err,
		"expected_unit_id", pending.unitID,
		"expected_function_code", pending.functionCode,
	)
}

func (a *Admission) logReject(message, clientID string, transactionID uint16, unitID, functionCode uint8, frameLen int, err error, extra ...any) {
	args := []any{
		"client_id", clientID,
		"transaction_id", transactionID,
		"unit_id", unitID,
		"function_code", functionCode,
		"frame_bytes", frameLen,
		"error", err,
	}
	args = append(args, extra...)
	a.logger.Warn(message, args...)
}

func compilePolicy(policy Policy) map[uint8]unitRule {
	units := make(map[uint8]unitRule, len(policy.Zones))
	for unitID, unitPolicy := range policy.Zones {
		functions := make(map[uint8]struct{}, len(unitPolicy.Functions))
		for _, functionCode := range unitPolicy.Functions {
			functions[functionCode] = struct{}{}
		}
		units[unitID] = unitRule{
			functions:   functions,
			allowWrites: unitPolicy.AllowWrites,
		}
	}
	return units
}

type mbapHeader struct {
	transactionID uint16
	unitID        uint8
}

// parseADU validates the MBAP envelope and returns its header plus the PDU.
// The MBAP length field covers the Unit ID and every PDU byte.
func parseADU(frame []byte) (mbapHeader, []byte, error) {
	if len(frame) < MBAPHeaderSize {
		return mbapHeader{}, nil, fmt.Errorf("%w: got=%d need_at_least=%d", ErrShortFrame, len(frame), MBAPHeaderSize)
	}

	transactionID := binary.BigEndian.Uint16(frame[0:2])
	protocolID := binary.BigEndian.Uint16(frame[2:4])
	length := int(binary.BigEndian.Uint16(frame[4:6]))
	unitID := frame[6]

	if protocolID != 0 {
		return mbapHeader{}, nil, fmt.Errorf("%w: got=%d", ErrProtocolID, protocolID)
	}

	actual := len(frame) - 6
	if length != actual {
		return mbapHeader{}, nil, fmt.Errorf("%w: header=%d actual=%d", ErrLengthMismatch, length, actual)
	}
	if length > maxMBAPLength {
		return mbapHeader{}, nil, fmt.Errorf("%w: length=%d max=%d", ErrOversizedFrame, length, maxMBAPLength)
	}
	if length < 2 {
		return mbapHeader{}, nil, fmt.Errorf("%w: length=%d need_at_least=2", ErrMissingPDU, length)
	}

	return mbapHeader{transactionID: transactionID, unitID: unitID}, frame[MBAPHeaderSize:], nil
}

// peekFields extracts fields that can safely be logged even when the envelope
// is malformed.
func peekFields(frame []byte) (transactionID uint16, unitID, functionCode uint8) {
	if len(frame) >= 2 {
		transactionID = binary.BigEndian.Uint16(frame[0:2])
	}
	if len(frame) >= MBAPHeaderSize {
		unitID = frame[6]
	}
	if len(frame) > MBAPHeaderSize {
		functionCode = frame[7]
	}
	return transactionID, unitID, functionCode
}
