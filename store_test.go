package deduct

import (
	"testing"
	"time"
)

var day0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(day, hour int) time.Time { return day0.AddDate(0, 0, day).Add(time.Duration(hour) * time.Hour) }

// 消费拆分:先消耗最早到期,到期相同再按 id。
func TestConsumeSplitOrder(t *testing.T) {
	s := NewStore()
	now := at(0, 9)
	s.Grant("food", 100, at(5, 0), now)  // id 1
	s.Grant("food", 100, at(2, 0), now)  // id 2, 最早到期
	s.Grant("food", 100, at(2, 0), now)  // id 3, 与 2 同期,按 id 排后
	s.Grant("drink", 100, at(1, 0), now) // id 4, 类别不适用

	ev, err := s.Consume("food", 150, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []SplitLine{{QuotaID: 2, Amount: 100}, {QuotaID: 3, Amount: 50}}
	if len(ev.Lines) != len(want) {
		t.Fatalf("lines = %+v, want %+v", ev.Lines, want)
	}
	for i, l := range ev.Lines {
		if l != want[i] {
			t.Fatalf("line %d = %+v, want %+v", i, l, want[i])
		}
	}
	if got := s.Balance("food", now); got != 150 {
		t.Fatalf("balance = %d, want 150", got)
	}
	// 不适用类别分文未动
	if q, _ := s.Quota(4); q.Remaining != 100 {
		t.Fatalf("drink quota touched: %d", q.Remaining)
	}
}

// 消费不可拆到不适用额度:类别不符与已过期额度都跳过,不足时整体失败且原子。
func TestConsumeSkipsInapplicable(t *testing.T) {
	s := NewStore()
	now := at(3, 9)
	s.Grant("food", 100, at(5, 0), now) // id 1, 可用
	s.Grant("food", 100, at(2, 0), now) // id 2, 已过期
	s.Grant("drink", 100, at(9, 0), now)

	if _, err := s.Consume("food", 101, now); err != ErrInsufficient {
		t.Fatalf("err = %v, want ErrInsufficient", err)
	}
	// 原子性:可用额度未被部分扣减
	if q, _ := s.Quota(1); q.Remaining != 100 {
		t.Fatalf("partial deduction leaked: %d", q.Remaining)
	}
	if _, err := s.Consume("food", 100, now); err != nil {
		t.Fatal(err)
	}
	if q, _ := s.Quota(2); q.Remaining != 100 {
		t.Fatalf("expired quota consumed: %d", q.Remaining)
	}
}

// 同日边界:到期时刻精确等于 now 即不可用,同日晚些时候到期仍可用。
func TestSameDayBoundary(t *testing.T) {
	s := NewStore()
	now := at(1, 12)
	s.Grant("food", 100, at(1, 12), now) // id 1, 恰好在 now 到期 -> 边界外
	s.Grant("food", 100, at(1, 23), now) // id 2, 同日晚些到期 -> 可用

	ev, err := s.Consume("food", 100, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Lines) != 1 || ev.Lines[0].QuotaID != 2 {
		t.Fatalf("lines = %+v, want only quota 2", ev.Lines)
	}
	if got := s.Balance("food", now); got != 0 {
		t.Fatalf("balance = %d, want 0 (quota 1 expired at boundary)", got)
	}
}

// 撤销消费:仍有效部分归还原额度,已过期部分仅记失效返还。
func TestRevokeRestoresAndRecordsExpired(t *testing.T) {
	s := NewStore()
	t0 := at(0, 9)
	s.Grant("food", 60, at(2, 0), t0) // id 1, day2 到期
	s.Grant("food", 40, at(9, 0), t0) // id 2, day9 到期

	ev, err := s.Consume("food", 100, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Balance("food", t0); got != 0 {
		t.Fatalf("balance after consume = %d, want 0", got)
	}

	// 推进到 day5:quota 1 已过期,quota 2 仍有效
	rev, err := s.Revoke(ev.Seq, at(5, 9))
	if err != nil {
		t.Fatal(err)
	}
	var restored, expired int64
	for _, l := range rev.Lines {
		restored += l.Restored
		expired += l.Expired
	}
	if restored != 40 || expired != 60 {
		t.Fatalf("restored=%d expired=%d, want 40/60", restored, expired)
	}
	if q, _ := s.Quota(1); q.Remaining != 0 {
		t.Fatalf("expired quota restored to usable: %d", q.Remaining)
	}
	if q, _ := s.Quota(2); q.Remaining != 40 {
		t.Fatalf("valid quota not restored: %d", q.Remaining)
	}
	if got := s.InvalidReturns(); got != 60 {
		t.Fatalf("invalid returns = %d, want 60", got)
	}
}

// 撤销先后与重复撤销:时间线账按序记录,重复撤销被拒绝。
func TestRevokeOrderAndDoubleRevoke(t *testing.T) {
	s := NewStore()
	t0 := at(0, 9)
	s.Grant("food", 100, at(9, 0), t0)

	c1, _ := s.Consume("food", 30, at(1, 9))
	c2, _ := s.Consume("food", 20, at(2, 9))

	// 先撤销后发生的 c2,再撤销 c1
	if _, err := s.Revoke(c2.Seq, at(3, 9)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(c1.Seq, at(4, 9)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(c1.Seq, at(4, 10)); err != ErrAlreadyRevoked {
		t.Fatalf("double revoke err = %v, want ErrAlreadyRevoked", err)
	}
	if _, err := s.Revoke(999, at(4, 10)); err != ErrNotFound {
		t.Fatalf("missing consume err = %v, want ErrNotFound", err)
	}

	// 时间线账:事件按发生顺序排列,撤销指向各自消费
	ledger := s.Ledger()
	var types []EventType
	for _, e := range ledger {
		types = append(types, e.Type)
	}
	want := []EventType{EventGrant, EventConsume, EventConsume, EventRevoke, EventRevoke}
	if len(types) != len(want) {
		t.Fatalf("ledger = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event %d = %s, want %s", i, types[i], want[i])
		}
	}
	if ledger[3].ConsumeSeq != c2.Seq || ledger[4].ConsumeSeq != c1.Seq {
		t.Fatalf("revoke order = %d,%d, want %d,%d",
			ledger[3].ConsumeSeq, ledger[4].ConsumeSeq, c2.Seq, c1.Seq)
	}
	if got := s.Balance("food", at(5, 0)); got != 100 {
		t.Fatalf("balance after revokes = %d, want 100", got)
	}
}
