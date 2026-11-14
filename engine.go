package netsec

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type counterKey struct {
	accountID string
	channel   string
	window    WindowKind
	startUnix int64
}

type accountRules struct {
	daily    int64
	monthly  int64
	channels map[string]Limits
}

type reservationRecord struct {
	reservation Reservation
	dayUnix     int64
	monthUnix   int64
}

// Engine is a concurrency-safe quota reservation engine.
//
// Reserve performs idempotency lookup, limit checks, and all counter updates
// while holding one lock. This makes the check-and-deduct operation atomic and
// prevents concurrent requests from overrunning a shared account limit.
type Engine struct {
	mu           sync.Mutex
	accounts     map[string]accountRules
	usage        map[counterKey]int64
	reservations map[string]*reservationRecord
	location     *time.Location
	clock        func() time.Time
}

// New creates an Engine from cfg. Configuration is copied and can be safely
// discarded by the caller after New returns.
func New(cfg Config) (*Engine, error) {
	location := cfg.Location
	if location == nil {
		location = time.UTC
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}

	accounts := make(map[string]accountRules, len(cfg.Accounts))
	for accountID, policy := range cfg.Accounts {
		if accountID == "" {
			return nil, fmt.Errorf("%w: account id is empty", ErrInvalidRequest)
		}
		if policy.DailyLimit < 0 || policy.MonthlyLimit < 0 {
			return nil, fmt.Errorf(
				"%w: account %q has a negative limit",
				ErrInvalidRequest,
				accountID,
			)
		}

		channels := make(map[string]Limits, len(policy.ChannelLimits))
		for channel, limits := range policy.ChannelLimits {
			if channel == "" {
				return nil, fmt.Errorf(
					"%w: account %q has an empty channel",
					ErrInvalidRequest,
					accountID,
				)
			}
			if limits.Daily < 0 || limits.Monthly < 0 {
				return nil, fmt.Errorf(
					"%w: account %q channel %q has a negative limit",
					ErrInvalidRequest,
					accountID,
					channel,
				)
			}
			channels[channel] = limits
		}

		accounts[accountID] = accountRules{
			daily:    policy.DailyLimit,
			monthly:  policy.MonthlyLimit,
			channels: channels,
		}
	}

	return &Engine{
		accounts:     accounts,
		usage:        make(map[counterKey]int64),
		reservations: make(map[string]*reservationRecord),
		location:     location,
		clock:        clock,
	}, nil
}

// NewEngine is an alias for New.
func NewEngine(cfg Config) (*Engine, error) {
	return New(cfg)
}

// Reserve atomically checks and deducts quota for a request.
//
// A successful call returns an active reservation. If the caller later fails to
// complete the protected operation, it must call Release with the same key.
func (e *Engine) Reserve(ctx context.Context, request ReserveRequest) (Reservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Reservation{}, err
	}
	if err := validateReserveRequest(request); err != nil {
		return Reservation{}, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Reservation{}, err
	}

	if existing, ok := e.reservations[request.IdempotencyKey]; ok {
		if !sameReservationRequest(existing.reservation, request) {
			return Reservation{}, &IdempotencyConflictError{
				Key: request.IdempotencyKey,
			}
		}
		snapshot := existing.reservation
		if !snapshot.Active() {
			return snapshot, fmt.Errorf(
				"%w: %q",
				ErrReservationReleased,
				request.IdempotencyKey,
			)
		}
		return snapshot, nil
	}

	rules, ok := e.accounts[request.AccountID]
	if !ok {
		return Reservation{}, fmt.Errorf(
			"%w: %q",
			ErrAccountNotFound,
			request.AccountID,
		)
	}

	at := request.At
	if at.IsZero() {
		at = e.clock()
	}
	if at.IsZero() {
		return Reservation{}, fmt.Errorf("%w: zero request time", ErrInvalidRequest)
	}

	dayStart, monthStart := windowStarts(at, e.location)
	totalDayKey := counterKey{
		accountID: request.AccountID,
		window:    WindowDaily,
		startUnix: dayStart.Unix(),
	}
	totalMonthKey := counterKey{
		accountID: request.AccountID,
		window:    WindowMonthly,
		startUnix: monthStart.Unix(),
	}
	channelDayKey := counterKey{
		accountID: request.AccountID,
		channel:   request.Channel,
		window:    WindowDaily,
		startUnix: dayStart.Unix(),
	}
	channelMonthKey := counterKey{
		accountID: request.AccountID,
		channel:   request.Channel,
		window:    WindowMonthly,
		startUnix: monthStart.Unix(),
	}

	if err := e.checkLimit(
		ScopeAccount,
		WindowDaily,
		totalDayKey,
		rules.daily,
		request.Amount,
	); err != nil {
		return Reservation{}, err
	}
	if err := e.checkLimit(
		ScopeAccount,
		WindowMonthly,
		totalMonthKey,
		rules.monthly,
		request.Amount,
	); err != nil {
		return Reservation{}, err
	}

	if channelLimits, exists := rules.channels[request.Channel]; exists {
		if err := e.checkLimit(
			ScopeChannel,
			WindowDaily,
			channelDayKey,
			channelLimits.Daily,
			request.Amount,
		); err != nil {
			return Reservation{}, err
		}
		if err := e.checkLimit(
			ScopeChannel,
			WindowMonthly,
			channelMonthKey,
			channelLimits.Monthly,
			request.Amount,
		); err != nil {
			return Reservation{}, err
		}
	}

	e.usage[totalDayKey] += request.Amount
	e.usage[totalMonthKey] += request.Amount
	e.usage[channelDayKey] += request.Amount
	e.usage[channelMonthKey] += request.Amount

	record := &reservationRecord{
		reservation: Reservation{
			IdempotencyKey: request.IdempotencyKey,
			AccountID:      request.AccountID,
			Channel:        request.Channel,
			Amount:         request.Amount,
			DayStart:       dayStart.In(e.location),
			MonthStart:     monthStart.In(e.location),
			Status:         ReservationActive,
		},
		dayUnix:   dayStart.Unix(),
		monthUnix: monthStart.Unix(),
	}
	e.reservations[request.IdempotencyKey] = record

	return record.reservation, nil
}

// Release returns reserved quota to both the account-wide and channel counters
// for the windows in which the reservation was created. Releasing the same key
// more than once is a no-op.
func (e *Engine) Release(ctx context.Context, idempotencyKey string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if idempotencyKey == "" {
		return fmt.Errorf("%w: idempotency key is empty", ErrInvalidRequest)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	record, ok := e.reservations[idempotencyKey]
	if !ok {
		return fmt.Errorf("%w: %q", ErrReservationNotFound, idempotencyKey)
	}
	if !record.reservation.Active() {
		return nil
	}

	e.decrement(counterKey{
		accountID: record.reservation.AccountID,
		window:    WindowDaily,
		startUnix: record.dayUnix,
	}, record.reservation.Amount)
	e.decrement(counterKey{
		accountID: record.reservation.AccountID,
		window:    WindowMonthly,
		startUnix: record.monthUnix,
	}, record.reservation.Amount)
	e.decrement(counterKey{
		accountID: record.reservation.AccountID,
		channel:   record.reservation.Channel,
		window:    WindowDaily,
		startUnix: record.dayUnix,
	}, record.reservation.Amount)
	e.decrement(counterKey{
		accountID: record.reservation.AccountID,
		channel:   record.reservation.Channel,
		window:    WindowMonthly,
		startUnix: record.monthUnix,
	}, record.reservation.Amount)

	record.reservation.Status = ReservationReleased
	return nil
}

// ReleaseReservation is a convenience wrapper around Release.
func (e *Engine) ReleaseReservation(ctx context.Context, reservation Reservation) error {
	return e.Release(ctx, reservation.IdempotencyKey)
}

// Usage returns a consistent snapshot of current quota consumption. Use an
// empty channel to query the account-wide total shared by all channels.
//
// Usage is intended for monitoring and diagnostics. Admission decisions must
// use Reserve, which performs the check and deduction atomically.
func (e *Engine) Usage(
	ctx context.Context,
	accountID string,
	channel string,
	at time.Time,
) (Usage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}
	if accountID == "" {
		return Usage{}, fmt.Errorf("%w: account id is empty", ErrInvalidRequest)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}

	rules, ok := e.accounts[accountID]
	if !ok {
		return Usage{}, fmt.Errorf("%w: %q", ErrAccountNotFound, accountID)
	}
	if at.IsZero() {
		at = e.clock()
	}
	if at.IsZero() {
		return Usage{}, fmt.Errorf("%w: zero usage time", ErrInvalidRequest)
	}

	dayStart, monthStart := windowStarts(at, e.location)
	dailyLimit := rules.daily
	monthlyLimit := rules.monthly
	if channel != "" {
		if limits, exists := rules.channels[channel]; exists {
			dailyLimit = limits.Daily
			monthlyLimit = limits.Monthly
		} else {
			dailyLimit = 0
			monthlyLimit = 0
		}
	}

	return Usage{
		AccountID:  accountID,
		Channel:    channel,
		DayStart:   dayStart.In(e.location),
		MonthStart: monthStart.In(e.location),
		DailyUsed: e.usage[counterKey{
			accountID: accountID,
			channel:   channel,
			window:    WindowDaily,
			startUnix: dayStart.Unix(),
		}],
		MonthlyUsed: e.usage[counterKey{
			accountID: accountID,
			channel:   channel,
			window:    WindowMonthly,
			startUnix: monthStart.Unix(),
		}],
		DailyLimit:   dailyLimit,
		MonthlyLimit: monthlyLimit,
	}, nil
}

func (e *Engine) checkLimit(
	scope Scope,
	window WindowKind,
	key counterKey,
	limit int64,
	amount int64,
) error {
	if limit == 0 {
		return nil
	}
	used := e.usage[key]
	if used > limit || amount > limit-used {
		return &QuotaExceededError{
			Scope:     scope,
			Window:    window,
			Limit:     limit,
			Used:      used,
			Requested: amount,
		}
	}
	return nil
}

func (e *Engine) decrement(key counterKey, amount int64) {
	remaining := e.usage[key] - amount
	if remaining <= 0 {
		delete(e.usage, key)
		return
	}
	e.usage[key] = remaining
}

func validateReserveRequest(request ReserveRequest) error {
	switch {
	case request.IdempotencyKey == "":
		return fmt.Errorf("%w: idempotency key is empty", ErrInvalidRequest)
	case request.AccountID == "":
		return fmt.Errorf("%w: account id is empty", ErrInvalidRequest)
	case request.Channel == "":
		return fmt.Errorf("%w: channel is empty", ErrInvalidRequest)
	case request.Amount <= 0:
		return fmt.Errorf("%w: amount must be positive", ErrInvalidRequest)
	default:
		return nil
	}
}

func sameReservationRequest(reservation Reservation, request ReserveRequest) bool {
	return reservation.AccountID == request.AccountID &&
		reservation.Channel == request.Channel &&
		reservation.Amount == request.Amount
}

func windowStarts(at time.Time, location *time.Location) (time.Time, time.Time) {
	local := at.In(location)
	year, month, day := local.Date()
	dayStart := time.Date(year, month, day, 0, 0, 0, 0, location)
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, location)
	return dayStart, monthStart
}
