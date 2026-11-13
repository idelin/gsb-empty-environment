package netsec

import (
	"bytes"
	"context"
	"fmt"
	"sync"
)

type memoryTicket struct {
	maxRedemptions int
	nextSequence   int
	records        map[string]*Record
}

// MemoryStore is an in-process implementation of RedemptionStore. It is useful
// as a reference store and for tests. Hosts that need process-crash durability
// should provide the same two-commit contract over their database.
type MemoryStore struct {
	mu      sync.Mutex
	tickets map[TicketID]*memoryTicket

	reserveCalls uint64
	commitCalls  uint64
	lookupCalls  uint64
}

type MemoryStoreStats struct {
	ReserveCalls uint64
	CommitCalls  uint64
	LookupCalls  uint64
	Tickets      int
	Reservations int
	Signatures   int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tickets: make(map[TicketID]*memoryTicket)}
}

func (s *MemoryStore) initLocked() {
	if s.tickets == nil {
		s.tickets = make(map[TicketID]*memoryTicket)
	}
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func cloneRecord(record Record) Record {
	record.Signature = append([]byte(nil), record.Signature...)
	return record
}

// Reserve atomically allocates a ticket sequence and stores the request's
// blind-value commitment. Existing requests return their durable record.
func (s *MemoryStore) Reserve(ctx context.Context, reservation Reservation) (ReserveResult, error) {
	if err := checkContext(ctx); err != nil {
		return ReserveResult{}, err
	}
	if reservation.MaxRedemptions < MinRedemptions || reservation.MaxRedemptions > MaxRedemptions {
		return ReserveResult{}, fmt.Errorf("%w: max redemptions must be between %d and %d", ErrInvalidPolicy, MinRedemptions, MaxRedemptions)
	}
	if reservation.RequestID == "" {
		return ReserveResult{}, fmt.Errorf("%w: empty request ID", ErrInvalidRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.reserveCalls++

	ticket, ok := s.tickets[reservation.TicketID]
	if !ok {
		ticket = &memoryTicket{
			maxRedemptions: reservation.MaxRedemptions,
			nextSequence:   1,
			records:        make(map[string]*Record),
		}
		s.tickets[reservation.TicketID] = ticket
	}

	if record, exists := ticket.records[reservation.RequestID]; exists {
		if record.ClaimDigest != reservation.ClaimDigest {
			return ReserveResult{}, fmt.Errorf(
				"%w: request %q was committed for another claim digest",
				ErrRequestConflict,
				reservation.RequestID,
			)
		}
		if !BlindDigestEqual(record.BlindDigest, reservation.BlindDigest) {
			return ReserveResult{}, fmt.Errorf(
				"%w: request %q",
				ErrBlindMismatch,
				reservation.RequestID,
			)
		}
		return ReserveResult{Record: cloneRecord(*record), Created: false}, nil
	}
	if ticket.maxRedemptions != reservation.MaxRedemptions {
		return ReserveResult{}, fmt.Errorf(
			"%w: ticket %s was committed with max=%d, got max=%d",
			ErrTicketPolicyMismatch,
			reservation.TicketID,
			ticket.maxRedemptions,
			reservation.MaxRedemptions,
		)
	}

	if ticket.nextSequence > ticket.maxRedemptions {
		return ReserveResult{}, fmt.Errorf("%w: ticket %s", ErrQuotaExceeded, reservation.TicketID)
	}

	record := &Record{
		TicketID:    reservation.TicketID,
		RequestID:   reservation.RequestID,
		ClaimDigest: reservation.ClaimDigest,
		BlindDigest: reservation.BlindDigest,
		Sequence:    ticket.nextSequence,
	}
	ticket.nextSequence++
	ticket.records[reservation.RequestID] = record

	return ReserveResult{Record: cloneRecord(*record), Created: true}, nil
}

// CommitSignature atomically records the private-key result for an existing
// reservation. It never creates a reservation and never changes its blind
// commitment.
func (s *MemoryStore) CommitSignature(ctx context.Context, completion Completion) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if completion.RequestID == "" {
		return fmt.Errorf("%w: empty request ID", ErrInvalidRequest)
	}
	if len(completion.Signature) == 0 {
		return fmt.Errorf("%w: empty signature", ErrInvalidSignature)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.commitCalls++

	ticket, ok := s.tickets[completion.TicketID]
	if !ok {
		return fmt.Errorf("%w: ticket %s", ErrReservationNotFound, completion.TicketID)
	}
	record, ok := ticket.records[completion.RequestID]
	if !ok {
		return fmt.Errorf("%w: request %q", ErrReservationNotFound, completion.RequestID)
	}
	if !BlindDigestEqual(record.BlindDigest, completion.BlindDigest) {
		return fmt.Errorf("%w: request %q", ErrBlindMismatch, completion.RequestID)
	}
	if record.Signature != nil {
		if bytes.Equal(record.Signature, completion.Signature) {
			return nil
		}
		return fmt.Errorf("%w: request %q", ErrSignatureConflict, completion.RequestID)
	}
	record.Signature = append([]byte(nil), completion.Signature...)
	return nil
}

func (s *MemoryStore) Lookup(ctx context.Context, ticketID TicketID, requestID string) (Record, bool, error) {
	if err := checkContext(ctx); err != nil {
		return Record{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.lookupCalls++

	ticket, ok := s.tickets[ticketID]
	if !ok {
		return Record{}, false, nil
	}
	record, ok := ticket.records[requestID]
	if !ok {
		return Record{}, false, nil
	}
	return cloneRecord(*record), true, nil
}

func (s *MemoryStore) Stats() MemoryStoreStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := MemoryStoreStats{
		ReserveCalls: s.reserveCalls,
		CommitCalls:  s.commitCalls,
		LookupCalls:  s.lookupCalls,
		Tickets:      len(s.tickets),
	}
	for _, ticket := range s.tickets {
		stats.Reservations += len(ticket.records)
		for _, record := range ticket.records {
			if record.Signature != nil {
				stats.Signatures++
			}
		}
	}
	return stats
}
