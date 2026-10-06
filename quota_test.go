package quotaledger

import (
	"reflect"
	"testing"
	"time"
)

// manualClock is an injectable clock for deterministic timelines.
type manualClock struct{ t time.Time }

func (m *manualClock) now() time.Time          { return m.t }
func (m *manualClock) set(t time.Time)         { m.t = t }
func (m *manualClock) advance(d time.Duration) { m.t = m.t.Add(d) }

var day0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func day(n int) time.Time { return day0.AddDate(0, 0, n) }

func newTestLedger() (*Ledger, *manualClock) {
	clk := &manualClock{t: day0}
	return NewLedger(clk.now), clk
}

func mustGrant(t *testing.T, l *Ledger, amount int64, cats []Category, exp time.Time) *Quota {
	t.Helper()
	q, err := l.Grant(amount, cats, exp)
	if err != nil {
		t.Fatalf("Grant(%d, %v): %v", amount, cats, err)
	}
	return q
}

func mustConsume(t *testing.T, l *Ledger, cat Category, amount int64) *Consumption {
	t.Helper()
	c, err := l.Consume(cat, amount)
	if err != nil {
		t.Fatalf("Consume(%s, %d): %v", cat, amount, err)
	}
	return c
}

func remaining(t *testing.T, l *Ledger, id int64) int64 {
	t.Helper()
	q, ok := l.Quota(id)
	if !ok {
		t.Fatalf("quota %d missing", id)
	}
	return q.Remaining
}

// TestConsumeSplitOrder verifies the timeline账 for a consumption split
// across quotas ordered by earliest expiry, then by ID.
func TestConsumeSplitOrder(t *testing.T) {
	l, _ := newTestLedger()
	// Same expiry -> ID order wins; later expiry drains last.
	q1 := mustGrant(t, l, 100, nil, day(10))
	q2 := mustGrant(t, l, 50, nil, day(5))
	q3 := mustGrant(t, l, 70, nil, day(5)) // same expiry as q2, larger ID

	c := mustConsume(t, l, "food", 160)

	wantSplits := []Split{
		{QuotaID: q2.ID, Amount: 50},
		{QuotaID: q3.ID, Amount: 70},
		{QuotaID: q1.ID, Amount: 40},
	}
	if !reflect.DeepEqual(c.Splits, wantSplits) {
		t.Fatalf("splits = %v, want %v", c.Splits, wantSplits)
	}
	if got := remaining(t, l, q1.ID); got != 60 {
		t.Fatalf("q1 remaining = %d, want 60", got)
	}

	// Timeline: 3 grants then 3 consume splits in drain order.
	entries := l.Entries()
	var consumeEntries []Entry
	for _, e := range entries {
		if e.Kind == EntryConsume {
			consumeEntries = append(consumeEntries, e)
		}
	}
	if len(consumeEntries) != 3 {
		t.Fatalf("consume entries = %d, want 3", len(consumeEntries))
	}
	for i, s := range wantSplits {
		e := consumeEntries[i]
		if e.QuotaID != s.QuotaID || e.Amount != s.Amount || e.ConsumeID != c.ID {
			t.Fatalf("entry[%d] = %+v, want quota=%d amount=%d consume=%d",
				i, e, s.QuotaID, s.Amount, c.ID)
		}
		if e.Seq <= consumeEntries[0].Seq && i > 0 {
			t.Fatalf("entry seq not increasing: %+v", consumeEntries)
		}
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].Seq != entries[i-1].Seq+1 {
			t.Fatalf("timeline seq gap at %d: %+v", i, entries)
		}
	}
}

// TestConsumeNeverSplitsIntoInapplicable verifies category matching and
// that inapplicable balances are untouched even when funds run short.
func TestConsumeNeverSplitsIntoInapplicable(t *testing.T) {
	l, _ := newTestLedger()
	food := mustGrant(t, l, 100, []Category{"food"}, day(10))
	travel := mustGrant(t, l, 100, []Category{"travel"}, day(3)) // earlier expiry
	any := mustGrant(t, l, 40, nil, day(20))                     // unrestricted

	c := mustConsume(t, l, "food", 120)
	want := []Split{{QuotaID: food.ID, Amount: 100}, {QuotaID: any.ID, Amount: 20}}
	if !reflect.DeepEqual(c.Splits, want) {
		t.Fatalf("splits = %v, want %v", c.Splits, want)
	}
	if got := remaining(t, l, travel.ID); got != 100 {
		t.Fatalf("travel quota touched: remaining = %d, want 100", got)
	}

	// Only 20 (any) + 100 (travel, inapplicable) left: food needs 30 > 20.
	if _, err := l.Consume("food", 30); err != ErrInsufficient {
		t.Fatalf("err = %v, want ErrInsufficient", err)
	}
	// Failed consume leaves no trace in the timeline.
	for _, e := range l.Entries() {
		if e.Kind == EntryConsume && e.Amount == 30 {
			t.Fatalf("failed consume leaked into timeline: %+v", e)
		}
	}
	if got := remaining(t, l, any.ID); got != 20 {
		t.Fatalf("any remaining = %d after failed consume, want 20", got)
	}
}

// TestSameDayBoundary pins the inclusive expiry rule: a quota is usable
// exactly at its expiry instant and unusable one tick after.
func TestSameDayBoundary(t *testing.T) {
	l, clk := newTestLedger()
	exp := day(7)
	q := mustGrant(t, l, 100, nil, exp)

	clk.set(exp.Add(-time.Second))
	if got := l.RemainingAt("food", clk.now()); got != 100 {
		t.Fatalf("remaining before expiry = %d, want 100", got)
	}

	// Exactly at the expiry instant: still usable.
	clk.set(exp)
	c, err := l.Consume("food", 60)
	if err != nil {
		t.Fatalf("consume at expiry instant: %v", err)
	}
	if len(c.Splits) != 1 || c.Splits[0].QuotaID != q.ID || c.Splits[0].Amount != 60 {
		t.Fatalf("splits at boundary = %v", c.Splits)
	}

	// One tick after: quota is expired and invisible to consumption.
	clk.set(exp.Add(time.Nanosecond))
	if got := l.RemainingAt("food", clk.now()); got != 0 {
		t.Fatalf("remaining after expiry = %d, want 0", got)
	}
	if _, err := l.Consume("food", 1); err != ErrInsufficient {
		t.Fatalf("consume after expiry err = %v, want ErrInsufficient", err)
	}
}

// TestRevokeOrdering verifies revocation returns portions to their
// original quotas, and that portions already expired at revoke time are
// booked as 失效返还 (REVOKE_EXPIRE) without restoring the balance.
func TestRevokeOrdering(t *testing.T) {
	l, clk := newTestLedger()
	short := mustGrant(t, l, 50, nil, day(3))
	long := mustGrant(t, l, 100, nil, day(30))

	c := mustConsume(t, l, "food", 120) // 50 from short, 70 from long
	if len(c.Splits) != 2 {
		t.Fatalf("splits = %v", c.Splits)
	}

	// Revoke after short's expiry but before long's.
	clk.set(day(10))
	if err := l.Revoke(c.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if got := remaining(t, l, short.ID); got != 0 {
		t.Fatalf("expired portion restored: short remaining = %d, want 0", got)
	}
	if got := remaining(t, l, long.ID); got != 100 {
		t.Fatalf("valid portion not restored: long remaining = %d, want 100", got)
	}

	// Timeline tail: expire entry for short, restore entry for long,
	// in the original split order.
	entries := l.Entries()
	tail := entries[len(entries)-2:]
	if tail[0].Kind != EntryRevokeExpire || tail[0].QuotaID != short.ID || tail[0].Amount != 50 {
		t.Fatalf("tail[0] = %+v, want REVOKE_EXPIRE short 50", tail[0])
	}
	if tail[1].Kind != EntryRevokeRestore || tail[1].QuotaID != long.ID || tail[1].Amount != 70 {
		t.Fatalf("tail[1] = %+v, want REVOKE_RESTORE long 70", tail[1])
	}
	if tail[0].ConsumeID != c.ID || tail[1].ConsumeID != c.ID {
		t.Fatalf("revoke entries not linked to consumption %d", c.ID)
	}

	// Double revoke is rejected and adds no entries.
	before := len(l.Entries())
	if err := l.Revoke(c.ID); err != ErrAlreadyRevoked {
		t.Fatalf("second revoke err = %v, want ErrAlreadyRevoked", err)
	}
	if len(l.Entries()) != before {
		t.Fatalf("double revoke mutated timeline")
	}
	if _, err := l.Consume("food", 100); err != nil {
		t.Fatalf("re-consume restored balance: %v", err)
	}
}

// TestRevokeBeforeExpiryRestoresEverything covers the fully-valid path
// and unknown-ID handling.
func TestRevokeBeforeExpiryRestoresEverything(t *testing.T) {
	l, _ := newTestLedger()
	q := mustGrant(t, l, 80, nil, day(5))
	c := mustConsume(t, l, "food", 80)

	if err := l.Revoke(c.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := remaining(t, l, q.ID); got != 80 {
		t.Fatalf("remaining = %d, want 80", got)
	}
	last := l.Entries()[len(l.Entries())-1]
	if last.Kind != EntryRevokeRestore || last.Amount != 80 {
		t.Fatalf("last entry = %+v, want REVOKE_RESTORE 80", last)
	}
	if err := l.Revoke(9999); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestRevokeAtExactExpiryInstant pins the boundary for revocation:
// at the expiry instant the quota is still valid, so the portion is
// restored; strictly after, it becomes 失效返还.
func TestRevokeAtExactExpiryInstant(t *testing.T) {
	l, clk := newTestLedger()
	exp := day(7)
	q := mustGrant(t, l, 60, nil, exp)
	c := mustConsume(t, l, "food", 60)

	clk.set(exp) // exactly at expiry: still valid
	if err := l.Revoke(c.ID); err != nil {
		t.Fatalf("revoke at expiry instant: %v", err)
	}
	if got := remaining(t, l, q.ID); got != 60 {
		t.Fatalf("remaining = %d, want 60 (restored at boundary)", got)
	}
	last := l.Entries()[len(l.Entries())-1]
	if last.Kind != EntryRevokeRestore {
		t.Fatalf("last entry = %v, want REVOKE_RESTORE", last.Kind)
	}

	// A second quota revoked strictly after expiry -> 失效返还.
	q2 := mustGrant(t, l, 30, nil, exp)
	clk.set(exp.Add(-time.Hour))
	// Same expiry as q, so the restore from c (60 on q) drains first by ID.
	c2 := mustConsume(t, l, "food", 90)
	wantSplits := []Split{{QuotaID: q.ID, Amount: 60}, {QuotaID: q2.ID, Amount: 30}}
	if !reflect.DeepEqual(c2.Splits, wantSplits) {
		t.Fatalf("c2 splits = %v, want %v", c2.Splits, wantSplits)
	}
	clk.set(exp.Add(time.Nanosecond))
	if err := l.Revoke(c2.ID); err != nil {
		t.Fatalf("revoke after expiry: %v", err)
	}
	if got := remaining(t, l, q.ID); got != 0 {
		t.Fatalf("q remaining = %d, want 0 (expired return)", got)
	}
	if got := remaining(t, l, q2.ID); got != 0 {
		t.Fatalf("q2 remaining = %d, want 0 (expired return)", got)
	}
	tail := l.Entries()[len(l.Entries())-2:]
	if tail[0].Kind != EntryRevokeExpire || tail[0].QuotaID != q.ID || tail[0].Amount != 60 {
		t.Fatalf("tail[0] = %+v, want REVOKE_EXPIRE q 60", tail[0])
	}
	if tail[1].Kind != EntryRevokeExpire || tail[1].QuotaID != q2.ID || tail[1].Amount != 30 {
		t.Fatalf("tail[1] = %+v, want REVOKE_EXPIRE q2 30", tail[1])
	}
}

// TestTimelineAudit cross-checks quota balances against a replay of the
// timeline ledger: GRANT adds, CONSUME subtracts, REVOKE_RESTORE adds,
// REVOKE_EXPIRE leaves balances untouched.
func TestTimelineAudit(t *testing.T) {
	l, clk := newTestLedger()
	a := mustGrant(t, l, 100, []Category{"food"}, day(2))
	b := mustGrant(t, l, 200, nil, day(20))
	c1 := mustConsume(t, l, "food", 150)
	clk.set(day(5)) // a expired, b valid
	if err := l.Revoke(c1.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	mustConsume(t, l, "food", 50)

	replay := map[int64]int64{}
	for _, e := range l.Entries() {
		switch e.Kind {
		case EntryGrant, EntryRevokeRestore:
			replay[e.QuotaID] += e.Amount
		case EntryConsume:
			replay[e.QuotaID] -= e.Amount
		case EntryRevokeExpire:
			// 失效返还: recorded but not restored.
		}
	}
	for _, id := range []int64{a.ID, b.ID} {
		if got := remaining(t, l, id); got != replay[id] {
			t.Fatalf("quota %d balance %d != timeline replay %d", id, got, replay[id])
		}
	}
	if replay[a.ID] != 0 || replay[b.ID] != 150 {
		t.Fatalf("replay = %v, want {%d:0 %d:150}", replay, a.ID, b.ID)
	}
}
