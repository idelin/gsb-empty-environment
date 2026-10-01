package cardinalguard

import "sort"

// Ledger is the byte-level book of record. Live-series bytes are keyed
// exactly like the live series index, so a verifier can prove the index and
// the ledger agree entry for entry. Folded, late and expired bytes are kept
// as separate running totals so byte conservation holds:
//
//	receivedBytes == liveBytes + overflowBytes + lateBytes + expiredBytes
type Ledger struct {
	live     map[string]int64
	Overflow int64
	Late     int64
	Expired  int64
}

func NewLedger() *Ledger {
	return &Ledger{live: make(map[string]int64)}
}

func (l *Ledger) Add(key string, n int64) { l.live[key] += n }

func (l *Ledger) Remove(key string) int64 {
	n := l.live[key]
	delete(l.live, key)
	return n
}

func (l *Ledger) Get(key string) int64 { return l.live[key] }

func (l *Ledger) LiveTotal() int64 {
	var total int64
	for _, v := range l.live {
		total += v
	}
	return total
}

func (l *Ledger) LiveKeys() []string {
	keys := make([]string, 0, len(l.live))
	for k := range l.live {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (l *Ledger) Total() int64 {
	return l.LiveTotal() + l.Overflow + l.Late + l.Expired
}
