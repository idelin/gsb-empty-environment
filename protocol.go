package netsec

import "time"

// TypeID is the type identification byte (first byte) of an IEC 60870-5 ASDU.
type TypeID uint8

// COT is the cause-of-transmission field of an ASDU.
type COT uint8

// IOA is a three-octet information object address.
type IOA uint32

// ConnID identifies one IEC 60870-5-104 transport connection (a TCP data
// channel). An application maps every accepted connection to a stable, unique
// ConnID so that select/execute pairing can be bound to the originating
// connection.
type ConnID string

const (
	// TypeSingleCommand is C_SC_NA_1: single command.
	TypeSingleCommand TypeID = 45
)

const (
	// COTActivation (activate) accompanies both the select and the execute
	// activation; the S/E bit distinguishes them.
	COTActivation COT = 4
	// COTCancellation (deactivation/cancel) tears down a pending selection.
	COTCancellation COT = 8
)

// Bit layout of the single command value octet (C_SC_NA_1).
const (
	seBit   byte = 0x80 // S/E: 1 = select, 0 = execute
	scsBit  byte = 0x01 // SCS (single command state): 0 = OFF, 1 = ON
	quMask  byte = 0x3E // QU (qualifier of command), bits 1..5
	quShift      = 1
)

// DefaultSelectTimeout is the window in which an execute must confirm a
// select. A selection that is not confirmed within this window is void.
const DefaultSelectTimeout = 30 * time.Second
