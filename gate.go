package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
)

// MBAP header: transaction ID (2) + protocol ID (2) + length (2) + unit ID (1).
const mbapHeaderLen = 7

// Write-class function codes denied unless the policy explicitly allows writes.
var writeFuncCodes = map[uint8]bool{5: true, 6: true, 15: true, 16: true, 23: true}

var (
	ErrFrameTooShort       = errors.New("netsec: frame shorter than MBAP header plus function code")
	ErrProtocolID          = errors.New("netsec: MBAP protocol identifier must be 0")
	ErrLengthMismatch      = errors.New("netsec: MBAP length field does not match PDU byte count")
	ErrUnitIDDenied        = errors.New("netsec: unit ID not permitted by zone policy")
	ErrFuncCodeDenied      = errors.New("netsec: function code not permitted by zone policy")
	ErrWriteDenied         = errors.New("netsec: write-class function code denied by default")
	ErrTransactionConflict = errors.New("netsec: transaction ID already in flight")
)

// Policy is the caller-supplied zone policy for the forwarding loop.
// Nil maps mean "no restriction" for that dimension; write-class function
// codes (5, 6, 15, 16, 23) are still denied unless AllowWrites is set.
type Policy struct {
	UnitIDs     map[uint8]bool
	FuncCodes   map[uint8]bool
	AllowWrites bool
}

func (p *Policy) admit(unitID, funcCode uint8) error {
	if writeFuncCodes[funcCode] && (p == nil || !p.AllowWrites) {
		return fmt.Errorf("%w: function code %d", ErrWriteDenied, funcCode)
	}
	if p == nil {
		return nil
	}
	if p.UnitIDs != nil && !p.UnitIDs[unitID] {
		return fmt.Errorf("%w: unit %d", ErrUnitIDDenied, unitID)
	}
	if p.FuncCodes != nil && !p.FuncCodes[funcCode] {
		return fmt.Errorf("%w: function code %d", ErrFuncCodeDenied, funcCode)
	}
	return nil
}

// Request is a validated client request ready to be forwarded to the serial side.
type Request struct {
	TransactionID uint16
	UnitID        uint8
	FuncCode      uint8
	PDU           []byte
	Raw           []byte
}

// parseMBAP validates the MBAP header and returns transaction ID, unit ID and PDU.
func parseMBAP(frame []byte) (txID uint16, unitID uint8, pdu []byte, err error) {
	if len(frame) < mbapHeaderLen+1 {
		return 0, 0, nil, fmt.Errorf("%w: got %d bytes", ErrFrameTooShort, len(frame))
	}
	if binary.BigEndian.Uint16(frame[2:4]) != 0 {
		return 0, 0, nil, fmt.Errorf("%w: got %d", ErrProtocolID, binary.BigEndian.Uint16(frame[2:4]))
	}
	length := int(binary.BigEndian.Uint16(frame[4:6]))
	if length != len(frame)-6 {
		return 0, 0, nil, fmt.Errorf("%w: field=%d actual=%d", ErrLengthMismatch, length, len(frame)-6)
	}
	return binary.BigEndian.Uint16(frame[0:2]), frame[6], frame[7:], nil
}

// AdmitRequest validates one client frame against the MBAP invariants and the
// zone policy. The forwarding loop must only forward requests it returns.
func AdmitRequest(frame []byte, p *Policy) (*Request, error) {
	txID, unitID, pdu, err := parseMBAP(frame)
	if err != nil {
		return nil, err
	}
	if err := p.admit(unitID, pdu[0]); err != nil {
		return nil, err
	}
	return &Request{
		TransactionID: txID,
		UnitID:        unitID,
		FuncCode:      pdu[0],
		PDU:           pdu,
		Raw:           frame,
	}, nil
}

// Response is a slave response bound to a previously admitted request.
type Response struct {
	TransactionID uint16
	UnitID        uint8
	FuncCode      uint8
	Exception     bool
	ExceptionCode uint8
	PDU           []byte
	Raw           []byte
}

type pendingEntry struct {
	unitID   uint8
	funcCode uint8
}

// Matcher binds serial-side responses to in-flight client requests so a
// response is only delivered to the client that issued the transaction.
type Matcher struct {
	mu      sync.Mutex
	pending map[uint16]pendingEntry
	logger  *log.Logger
}

// NewMatcher returns a Matcher. A nil logger discards drop records.
func NewMatcher(logger *log.Logger) *Matcher {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Matcher{pending: make(map[uint16]pendingEntry), logger: logger}
}

// Track registers an admitted request before it is forwarded. It fails when
// the transaction ID is already in flight, so two clients can never share a
// transaction on the serial bus.
func (m *Matcher) Track(req *Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pending[req.TransactionID]; ok {
		return fmt.Errorf("%w: %d", ErrTransactionConflict, req.TransactionID)
	}
	m.pending[req.TransactionID] = pendingEntry{unitID: req.UnitID, funcCode: req.FuncCode}
	return nil
}

// Untrack forgets a transaction, e.g. after a serial-side timeout.
func (m *Matcher) Untrack(transactionID uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, transactionID)
}

// MatchResponse validates a serial-side frame and binds it to a pending
// request. It returns the response only when transaction ID, unit ID and
// function code (or function code|0x80 for exceptions) all match; anything
// else is dropped and recorded in the log.
func (m *Matcher) MatchResponse(frame []byte) (*Response, bool) {
	txID, unitID, pdu, err := parseMBAP(frame)
	if err != nil {
		m.drop("malformed response dropped: %v", err)
		return nil, false
	}

	m.mu.Lock()
	entry, ok := m.pending[txID]
	m.mu.Unlock()
	if !ok {
		m.drop("response dropped: no pending transaction %d", txID)
		return nil, false
	}
	if unitID != entry.unitID {
		m.drop("response dropped: transaction %d unit ID %d, want %d", txID, unitID, entry.unitID)
		return nil, false
	}

	funcCode := pdu[0]
	exception := false
	switch {
	case funcCode == entry.funcCode:
	case funcCode == entry.funcCode|0x80:
		exception = true
	default:
		m.drop("response dropped: transaction %d function code %#02x, want %#02x", txID, funcCode, entry.funcCode)
		return nil, false
	}

	m.mu.Lock()
	delete(m.pending, txID)
	m.mu.Unlock()

	resp := &Response{
		TransactionID: txID,
		UnitID:        unitID,
		FuncCode:      entry.funcCode,
		Exception:     exception,
		PDU:           pdu,
		Raw:           frame,
	}
	if exception && len(pdu) >= 2 {
		resp.ExceptionCode = pdu[1]
	}
	return resp, true
}

func (m *Matcher) drop(format string, args ...any) {
	m.logger.Printf(format, args...)
}
