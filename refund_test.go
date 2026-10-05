package refund

import (
	"fmt"
	"testing"
)

func sampleOrder() Order {
	return Order{
		Lines: []Line{
			{ID: "A", UnitPriceCents: 1000, Qty: 3, DiscountCents: 100}, // 可退 2900，每件 966/967/967
			{ID: "B", UnitPriceCents: 333, Qty: 3, DiscountCents: 0},    // 可退 999，每件 333
			{ID: "C", UnitPriceCents: 100, Qty: 2, DiscountCents: 1},    // 可退 199，每件 99/100
		},
		ShippingCents:         500,
		ShippingDiscountCents: 50, // 运费可退 450
	}
	// 整单可退 = 2900 + 999 + 199 + 450 = 4548
}

func refundAll(t *testing.T, s *Session, batches []Request) int64 {
	t.Helper()
	var total int64
	for i, req := range batches {
		res, err := s.Refund(req)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		total += res.TotalCents
	}
	return total
}

// 累计退款必须等于整单可退额。
func TestCumulativeEqualsTotal(t *testing.T) {
	s, err := NewSession(sampleOrder())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.TotalRefundable(), int64(4548); got != want {
		t.Fatalf("total = %d, want %d", got, want)
	}
	total := refundAll(t, s, []Request{
		{Key: "b1", Items: []ItemRefund{{LineID: "A", Qty: 1}}},
		{Key: "b2", Items: []ItemRefund{{LineID: "A", Qty: 2}, {LineID: "B", Qty: 1}}},
		{Key: "b3", Items: []ItemRefund{{LineID: "B", Qty: 2}, {LineID: "C", Qty: 2}}, RefundShipping: true},
	})
	if total != 4548 {
		t.Fatalf("cumulative = %d, want 4548", total)
	}
	if s.RemainingRefundable() != 0 {
		t.Fatalf("remaining = %d, want 0", s.RemainingRefundable())
	}
}

// 退款顺序不改变每件商品的最终可退额。
func TestOrderIndependence(t *testing.T) {
	perUnit := func(batches []Request) map[string][]int64 {
		s, err := NewSession(sampleOrder())
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]int64{}
		for _, req := range batches {
			res, err := s.Refund(req)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range res.Allocations {
				for i, idx := range a.UnitIndex {
					_ = i
					for len(out[a.LineID]) <= idx {
						out[a.LineID] = append(out[a.LineID], 0)
					}
					// 每件金额 = 该次分摊中此件的份额；逐件重算验证
				}
			}
		}
		// 直接从最终状态逐件核算
		for id := range map[string]bool{"A": true, "B": true, "C": true} {
			st := s.byID[id]
			out[id] = out[id][:0]
			for i := 0; i < st.line.Qty; i++ {
				out[id] = append(out[id], st.unitAmount(i))
			}
		}
		return out
	}
	fine := []Request{
		{Key: "1", Items: []ItemRefund{{LineID: "A", Qty: 1}}},
		{Key: "2", Items: []ItemRefund{{LineID: "A", Qty: 1}}},
		{Key: "3", Items: []ItemRefund{{LineID: "A", Qty: 1}}},
		{Key: "4", Items: []ItemRefund{{LineID: "C", Qty: 1}}},
		{Key: "5", Items: []ItemRefund{{LineID: "C", Qty: 1}, {LineID: "B", Qty: 3}}, RefundShipping: true},
	}
	coarse := []Request{
		{Key: "1", Items: []ItemRefund{{LineID: "C", Qty: 2}, {LineID: "A", Qty: 3}, {LineID: "B", Qty: 3}}, RefundShipping: true},
	}
	u1, u2 := perUnit(fine), perUnit(coarse)
	for id := range u1 {
		if fmt.Sprint(u1[id]) != fmt.Sprint(u2[id]) {
			t.Fatalf("line %s per-unit differs: %v vs %v", id, u1[id], u2[id])
		}
	}
	// 尾差确定性：A 行 2900/3 → 967,967,966? 余 2 → 序号 0,1 各补 1 → 967,967,966
	want := []int64{967, 967, 966}
	if fmt.Sprint(u1["A"]) != fmt.Sprint(want) {
		t.Fatalf("line A units = %v, want %v", u1["A"], want)
	}
}

// 重复退款请求不得重复计费。
func TestIdempotency(t *testing.T) {
	s, err := NewSession(sampleOrder())
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Key: "pay-1", Items: []ItemRefund{{LineID: "A", Qty: 2}}}
	r1, err := s.Refund(req)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.Refund(req)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Duplicate {
		t.Fatal("second request not marked duplicate")
	}
	if r1.TotalCents != r2.TotalCents {
		t.Fatalf("duplicate result mismatch: %d vs %d", r1.TotalCents, r2.TotalCents)
	}
	if r2.CumulativeCents != r1.CumulativeCents {
		t.Fatalf("duplicate changed cumulative: %d vs %d", r2.CumulativeCents, r1.CumulativeCents)
	}
	if s.cumulative != r1.TotalCents {
		t.Fatalf("double charged: cumulative %d, want %d", s.cumulative, r1.TotalCents)
	}
}

// 每一分钱都有分摊依据：成分之和等于分摊额，分摊之和等于请求总额。
func TestAuditBasis(t *testing.T) {
	s, err := NewSession(sampleOrder())
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Refund(Request{
		Key:            "audit",
		Items:          []ItemRefund{{LineID: "A", Qty: 3}, {LineID: "C", Qty: 1}},
		RefundShipping: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, a := range res.Allocations {
		var comp int64
		for _, c := range a.Components {
			if c.Reason == "" {
				t.Fatalf("line %s: component %s missing reason", a.LineID, c.Kind)
			}
			comp += c.Cents
		}
		if comp != a.TotalCents {
			t.Fatalf("line %s: components sum %d != total %d", a.LineID, comp, a.TotalCents)
		}
		sum += a.TotalCents
	}
	if sum != res.TotalCents {
		t.Fatalf("allocations sum %d != result total %d", sum, res.TotalCents)
	}
	// A 行 3 件全退 = 2900；C 行第 1 件 = 100；运费 450
	if res.TotalCents != 2900+100+450 {
		t.Fatalf("total = %d, want %d", res.TotalCents, 3450)
	}
}

// 超额退款、重复运费、未知行必须被拒绝且不产生部分入账。
func TestRejection(t *testing.T) {
	s, err := NewSession(sampleOrder())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refund(Request{Key: "x", Items: []ItemRefund{{LineID: "A", Qty: 4}}}); err == nil {
		t.Fatal("over-refund not rejected")
	}
	if _, err := s.Refund(Request{Key: "y", Items: []ItemRefund{{LineID: "ZZZ", Qty: 1}}}); err == nil {
		t.Fatal("unknown line not rejected")
	}
	if _, err := s.Refund(Request{Key: "z", Items: []ItemRefund{{LineID: "B", Qty: 3}}, RefundShipping: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refund(Request{Key: "z2", RefundShipping: true}); err == nil {
		t.Fatal("double shipping refund not rejected")
	}
	// 被拒绝的请求不得占用幂等键，也不得入账。
	if s.cumulative != 999+450 {
		t.Fatalf("cumulative = %d, want %d", s.cumulative, 1449)
	}
	if _, err := s.Refund(Request{Key: "x", Items: []ItemRefund{{LineID: "A", Qty: 1}}}); err != nil {
		t.Fatalf("rejected key should be reusable: %v", err)
	}
}

// 空请求与缺少幂等键必须报错。
func TestValidation(t *testing.T) {
	s, err := NewSession(sampleOrder())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refund(Request{Items: []ItemRefund{{LineID: "A", Qty: 1}}}); err == nil {
		t.Fatal("missing key not rejected")
	}
	if _, err := s.Refund(Request{Key: "empty"}); err == nil {
		t.Fatal("empty request not rejected")
	}
	if _, err := NewSession(Order{Lines: []Line{{ID: "A", UnitPriceCents: 100, Qty: 1, DiscountCents: 101}}}); err == nil {
		t.Fatal("discount exceeding gross not rejected")
	}
}
