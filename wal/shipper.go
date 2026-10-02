package wal

import "sync"

// LossyLink emulates an unreliable network link for log shipping: it
// deterministically "drops" roughly DropPercent of all frames. The pattern
// is a hash of the send sequence number, so tests are reproducible while
// retries still observe varying outcomes.
type LossyLink struct {
	mu          sync.Mutex
	DropPercent int // 0..100
	sent        int
	Dropped     int
	Delivered   int
}

// NewLossyLink returns a link dropping approximately pct percent of frames.
func NewLossyLink(pct int) *LossyLink { return &LossyLink{DropPercent: pct} }

// Send transmits one frame; it returns ErrPacketLoss when the frame is
// dropped by the emulated network.
func (l *LossyLink) Send(frame []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent++
	if l.DropPercent > 0 &&
		int((uint64(l.sent)*2654435761)%100) < l.DropPercent {
		l.Dropped++
		return ErrPacketLoss
	}
	l.Delivered++
	return nil
}

// Receiver is the replica side of log shipping. Apply is idempotent:
// records are deduplicated by LSN, so retransmissions are harmless.
type Receiver struct {
	mu   sync.Mutex
	seen map[uint64]bool
	Wal  *WAL
}

// NewReceiver returns a Receiver writing into the given replica WAL.
func NewReceiver(w *WAL) *Receiver {
	return &Receiver{seen: make(map[uint64]bool), Wal: w}
}

// Apply delivers one record to the replica, ignoring duplicates.
func (r *Receiver) Apply(rec *Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[rec.LSN] {
		return
	}
	r.seen[rec.LSN] = true
	cp := *rec
	r.Wal.WriteRaw(&cp)
}

// Shipper ships committed log records from a primary WAL to a Receiver
// over a LossyLink, retransmitting until every record is acknowledged.
type Shipper struct {
	Source     *WAL
	Link       *LossyLink
	MaxRetries int // per record; Sync fails if exceeded
}

// Sync ships every record of the source WAL to recv, in LSN order, with
// retransmission on packet loss. It returns ErrPacketLoss if a record
// cannot be delivered within MaxRetries attempts.
func (s *Shipper) Sync(recv *Receiver) error {
	maxRetries := s.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 64
	}
	for _, rec := range s.Source.Records() {
		attempts := 0
		for {
			if err := s.Link.Send(rec.Encode()); err == nil {
				recv.Apply(rec) // delivery + implicit ACK
				break
			}
			attempts++
			if attempts > maxRetries {
				return ErrPacketLoss
			}
		}
	}
	return nil
}
