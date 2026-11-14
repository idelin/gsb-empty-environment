package netsec

import "time"

// Limits describes a daily and monthly quota. A zero value means that the
// corresponding window is not capped at this level.
type Limits struct {
	Daily   int64
	Monthly int64
}

// AccountPolicy contains the limits for one account. DailyLimit and
// MonthlyLimit are shared by every channel. ChannelLimits adds optional,
// channel-specific limits on top of the shared account limits.
type AccountPolicy struct {
	DailyLimit    int64
	MonthlyLimit  int64
	ChannelLimits map[string]Limits
}

// Config configures an Engine.
//
// Location determines calendar day and month boundaries. It defaults to UTC.
// Clock is used when a request does not carry an explicit time and defaults to
// time.Now.
type Config struct {
	Accounts map[string]AccountPolicy
	Location *time.Location
	Clock    func() time.Time
}

// ReserveRequest asks the quota engine to atomically reserve Amount for an
// account and channel.
//
// IdempotencyKey identifies the business request. Reusing a key with the same
// account, channel, and amount returns the original reservation without
// deducting quota again. At selects the daily and monthly windows; a zero value
// uses the engine clock.
type ReserveRequest struct {
	IdempotencyKey string
	AccountID      string
	Channel        string
	Amount         int64
	At             time.Time
}

// ReservationStatus is the lifecycle state of a reservation.
type ReservationStatus string

const (
	ReservationActive   ReservationStatus = "active"
	ReservationReleased ReservationStatus = "released"
)

// Reservation is an immutable snapshot returned by Reserve.
type Reservation struct {
	IdempotencyKey string
	AccountID      string
	Channel        string
	Amount         int64
	DayStart       time.Time
	MonthStart     time.Time
	Status         ReservationStatus
}

// Active reports whether the reservation still consumes quota.
func (r Reservation) Active() bool {
	return r.Status == ReservationActive
}

// Usage is a point-in-time view of quota consumption.
//
// With an empty Channel, DailyUsed and MonthlyUsed are the account-wide totals
// and the limits are the account-wide limits. With a non-empty Channel, the
// values describe that channel and its optional channel-specific limits.
type Usage struct {
	AccountID    string
	Channel      string
	DayStart     time.Time
	MonthStart   time.Time
	DailyUsed    int64
	MonthlyUsed  int64
	DailyLimit   int64
	MonthlyLimit int64
}
