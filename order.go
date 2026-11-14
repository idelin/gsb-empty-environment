package netsec

import (
	"errors"
	"time"
)

// Side is the direction of an order.
type Side int

const (
	// SideUnset is the zero value and is never a valid order side.
	SideUnset Side = iota
	// SideBuy is a bid.
	SideBuy
	// SideSell is an ask.
	SideSell
)

// String returns a stable textual representation of the side.
func (s Side) String() string {
	switch s {
	case SideBuy:
		return "buy"
	case SideSell:
		return "sell"
	default:
		return "unset"
	}
}

// OrderStatus describes the lifecycle state of an accepted order.
type OrderStatus int

const (
	// StatusLive means the order rests on the book and can match.
	StatusLive OrderStatus = iota
	// StatusCancelled means the order was removed by Cancel.
	StatusCancelled
	// StatusFilled means the order was removed by Pop during matching.
	StatusFilled
)

// OrderRequest is an inbound order submission as presented by a client.
//
// ClientSeq and ClientTime are carried for audit only. They are deliberately
// absent from all ordering decisions; only the engine-assigned Sequence on the
// returned Order matters for time priority.
type OrderRequest struct {
	// Symbol identifies the traded instrument for this book.
	Symbol string
	// Owner is the submitting account identifier.
	Owner string
	// OrderID is the client-assigned unique identifier.
	OrderID string
	Side    Side
	// Price is a positive integer tick price.
	Price int64
	// Qty is a positive integer quantity.
	Qty int64
	// ClientSeq is a client-declared sequence number; never used for sorting.
	ClientSeq uint64
	// ClientTime is a client-declared timestamp; never used for sorting.
	ClientTime time.Time
}

// Order is an accepted resting order. Sequence is assigned by the engine and
// is the sole basis of time priority inside a price level.
type Order struct {
	Sequence uint64
	Status   OrderStatus
	req      OrderRequest
}

// Request returns the inbound request associated with the order.
func (o *Order) Request() OrderRequest { return o.req }

// Symbol, Owner, OrderID, Side, Price and Qty expose the request fields.
func (o *Order) Symbol() string  { return o.req.Symbol }
func (o *Order) Owner() string   { return o.req.Owner }
func (o *Order) OrderID() string { return o.req.OrderID }
func (o *Order) Side() Side      { return o.req.Side }
func (o *Order) Price() int64    { return o.req.Price }
func (o *Order) Qty() int64      { return o.req.Qty }

// ClientSeq returns the client-declared sequence number (audit only).
func (o *Order) ClientSeq() uint64 { return o.req.ClientSeq }

// ClientTime returns the client-declared timestamp (audit only).
func (o *Order) ClientTime() time.Time { return o.req.ClientTime }

// Validation errors returned by Submit before any sequence is consumed.
var (
	// ErrEmptyOrderID is returned when OrderID is blank.
	ErrEmptyOrderID = errors.New("netsec: order id is required")
	// ErrEmptyOwner is returned when Owner is blank.
	ErrEmptyOwner = errors.New("netsec: owner is required")
	// ErrInvalidSide is returned when the side is neither buy nor sell.
	ErrInvalidSide = errors.New("netsec: invalid order side")
	// ErrInvalidPrice is returned when Price is not strictly positive.
	ErrInvalidPrice = errors.New("netsec: price must be positive")
	// ErrInvalidQty is returned when Qty is not strictly positive.
	ErrInvalidQty = errors.New("netsec: qty must be positive")
	// ErrDuplicateOrderID is returned for a live order with the same id.
	ErrDuplicateOrderID = errors.New("netsec: duplicate live order id")
)

func (r OrderRequest) validate() error {
	if r.OrderID == "" {
		return ErrEmptyOrderID
	}
	if r.Owner == "" {
		return ErrEmptyOwner
	}
	if r.Side != SideBuy && r.Side != SideSell {
		return ErrInvalidSide
	}
	if r.Price <= 0 {
		return ErrInvalidPrice
	}
	if r.Qty <= 0 {
		return ErrInvalidQty
	}
	return nil
}
