package netsec

import (
	"net/netip"
	"sync"
	"time"
)

// IPv4PacketID identifies an IPv4 packet this host sent, parsed from the
// IP header and the first 8 bytes of payload (transport ports when present).
type IPv4PacketID struct {
	Src      netip.Addr
	Dst      netip.Addr
	Protocol uint8
	SrcPort  uint16 // 0 when the protocol carries no ports
	DstPort  uint16
}

// SentLog remembers recently transmitted packets so that the original IP
// header quoted inside an ICMP error can be matched against traffic this
// host actually generated.
type SentLog struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[IPv4PacketID]time.Time
}

func NewSentLog(ttl time.Duration) *SentLog {
	return &SentLog{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[IPv4PacketID]time.Time),
	}
}

// Record notes that id was transmitted at the current time.
func (l *SentLog) Record(id IPv4PacketID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[id] = l.now()
}

// Seen reports whether id was recorded within the log's TTL.
func (l *SentLog) Seen(id IPv4PacketID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	sentAt, ok := l.entries[id]
	if !ok {
		return false
	}
	return l.now().Sub(sentAt) <= l.ttl
}
