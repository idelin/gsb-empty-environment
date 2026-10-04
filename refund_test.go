package refund

import (
	"strings"
	"testing"
)

func sampleOrder() Order {
	return Order{
		ID: "order-1",
		Lines: []Line{
			{ID: "item-a", Kind: LineKindItem, Qty: 3, Paid: 1000, Discount: 0},  // 可退 1000，3 件，尾差 1
			{ID: "item-b", Kind: LineKindItem, Qty: 2, Paid: 500, Discount: 100}, // 可退 400，2 件，整除
			{ID: "ship", Kind: LineKindShipping, Paid: 200, Discount: 50},        // 可退 150
		},
	}
}

func mustCalc(t *testing.T, o Order) *Calculator {
	t.Helper()
	c, err := NewCalculator(o)
	if err != nil {
		t.Fatalf("NewCalculator: %v", err)
	}
	return c
}

func mustApply(t *testing.T, c *Calculator, req Request) Result {
	t.Helper()
	res, err := c.Apply(req)
	if err != nil {
		t.Fatalf("Apply(%s): %v", req.RequestID, err)
	}
	return res
}

// 尾差确定性：1000 分退 3 件，累计公式 floor(1000*q/3) = 333, 666, 1000，
// 尾差 1 分确定性地落在最后一件上，且累计恰好等于可退额。
func TestRemainderDeterministic(t *testing.T) {
	c := mustCalc(t, sampleOrder())
	want := []Money{333, 333, 334}
	for i, w := range want {
		res := mustApply(t, c, Request{
			RequestID: strings.Repeat("r", 1) + string(rune('1'+i)),
			Lines:     []LineRefund{{LineID: "item-a", Qty: 1}},
		})
		if res.Lines[0].ThisRefund != w {
			t.Fatalf("batch %d: got %d, want %d", i, res.Lines[0].ThisRefund, w)
		}
	}
	if got := c.TotalRefunded(); got != 1000 {
		t.Fatalf("total refunded = %d, want 1000", got)
	}
}

// 退款顺序无关：同一行按 [1,1,1]、[3]、[2,1] 三种分批方式退完，
// 每行最终累计退款额必须一致且等于可退额。
func TestOrderIndependence(t *testing.T) {
	seqs := [][]int64{{1, 1, 1}, {3}, {2, 1}, {1, 2}}
	var finals []Money
	for _, seq := range seqs {
		c := mustCalc(t, sampleOrder())
		for i, q := range seq {
			mustApply(t, c, Request{
				RequestID: "req-" + string(rune('a'+i)),
				Lines:     []LineRefund{{LineID: "item-a", Qty: q}},
			})
		}
		var final Money
		for _, ls := range c.Progress() {
			if ls.LineID == "item-a" {
				final = ls.CumulativeRefunded
			}
		}
		finals = append(finals, final)
	}
	for i, f := range finals {
		if f != 1000 {
			t.Fatalf("seq %v: final cumulative = %d, want 1000", seqs[i], f)
		}
	}
}

// 顺序无关的更强形式：任意中间累计件数 q 对应的累计金额只取决于 q。
func TestCumulativeDependsOnlyOnQty(t *testing.T) {
	// 路径一：先退 1 件再退 1 件，累计 2 件时的累计金额。
	c1 := mustCalc(t, sampleOrder())
	mustApply(t, c1, Request{RequestID: "x1", Lines: []LineRefund{{LineID: "item-a", Qty: 1}}})
	r := mustApply(t, c1, Request{RequestID: "x2", Lines: []LineRefund{{LineID: "item-a", Qty: 1}}})
	cum1 := r.Lines[0].CumulativeRefunded

	// 路径二：一次性退 2 件。
	c2 := mustCalc(t, sampleOrder())
	r2 := mustApply(t, c2, Request{RequestID: "y1", Lines: []LineRefund{{LineID: "item-a", Qty: 2}}})
	cum2 := r2.Lines[0].CumulativeRefunded

	if cum1 != cum2 || cum1 != 666 {
		t.Fatalf("cumulative at q=2: path1=%d path2=%d, want both 666", cum1, cum2)
	}
}

// 幂等：重复提交相同请求不重复计费。
func TestIdempotentReplay(t *testing.T) {
	c := mustCalc(t, sampleOrder())
	req := Request{RequestID: "req-1", Lines: []LineRefund{{LineID: "item-a", Qty: 1}}}
	r1 := mustApply(t, c, req)
	r2 := mustApply(t, c, req)
	if r2.Duplicate != true {
		t.Fatal("second apply should be marked duplicate")
	}
	if r1.Total != r2.Total || r1.Total != 333 {
		t.Fatalf("totals: first=%d second=%d", r1.Total, r2.Total)
	}
	if got := c.TotalRefunded(); got != 333 {
		t.Fatalf("total refunded = %d, want 333 (no double charge)", got)
	}
	// 行顺序不同但内容相同，也视为重复。
	reqReordered := Request{RequestID: "req-2", Lines: []LineRefund{
		{LineID: "item-a", Qty: 1}, {LineID: "item-b", Qty: 1},
	}}
	mustApply(t, c, reqReordered)
	dup := mustApply(t, c, Request{RequestID: "req-2", Lines: []LineRefund{
		{LineID: "item-b", Qty: 1}, {LineID: "item-a", Qty: 1},
	}})
	if !dup.Duplicate {
		t.Fatal("reordered identical request should be duplicate")
	}
}

// 同一 RequestID 携带不同内容必须报冲突。
func TestConflictingRequestID(t *testing.T) {
	c := mustCalc(t, sampleOrder())
	mustApply(t, c, Request{RequestID: "req-1", Lines: []LineRefund{{LineID: "item-a", Qty: 1}}})
	_, err := c.Apply(Request{RequestID: "req-1", Lines: []LineRefund{{LineID: "item-a", Qty: 2}}})
	if err != ErrRequestConflict {
		t.Fatalf("got %v, want ErrRequestConflict", err)
	}
}

// 超量退款被拒绝，且请求整体不生效（不产生部分退款）。
func TestOverRefundRejected(t *testing.T) {
	c := mustCalc(t, sampleOrder())
	_, err := c.Apply(Request{RequestID: "bad", Lines: []LineRefund{{LineID: "item-a", Qty: 4}}})
	if err == nil {
		t.Fatal("expected over-refund error")
	}
	// 部分行合法、部分超量：整体拒绝。
	_, err = c.Apply(Request{RequestID: "bad2", Lines: []LineRefund{
		{LineID: "item-b", Qty: 1},
		{LineID: "item-a", Qty: 4},
	}})
	if err == nil {
		t.Fatal("expected over-refund error")
	}
	if got := c.TotalRefunded(); got != 0 {
		t.Fatalf("failed request must not take effect, total refunded = %d", got)
	}
}

// 运费按金额退，超出可退额的部分被截断，累计恰好等于可退额。
func TestShippingCapped(t *testing.T) {
	c := mustCalc(t, sampleOrder())
	r1 := mustApply(t, c, Request{RequestID: "s1", Lines: []LineRefund{{LineID: "ship", Amount: 100}}})
	if r1.Lines[0].ThisRefund != 100 {
		t.Fatalf("got %d, want 100", r1.Lines[0].ThisRefund)
	}
	r2 := mustApply(t, c, Request{RequestID: "s2", Lines: []LineRefund{{LineID: "ship", Amount: 100}}})
	if r2.Lines[0].ThisRefund != 50 || r2.Lines[0].CumulativeRefunded != 150 || r2.Lines[0].Remaining != 0 {
		t.Fatalf("shipping should cap at refundable: %+v", r2.Lines[0])
	}
}

// 整单退完：累计退款 == 整单可退额，分文不差。
func TestFullRefundEqualsTotalRefundable(t *testing.T) {
	o := sampleOrder()
	c := mustCalc(t, o)
	mustApply(t, c, Request{RequestID: "f1", Lines: []LineRefund{
		{LineID: "item-a", Qty: 2},
		{LineID: "ship", Amount: 150},
	}})
	mustApply(t, c, Request{RequestID: "f2", Lines: []LineRefund{
		{LineID: "item-a", Qty: 1},
		{LineID: "item-b", Qty: 2},
	}})
	if !c.FullyRefunded() {
		t.Fatalf("total refunded = %d, want %d", c.TotalRefunded(), o.TotalRefundable())
	}
	for _, ls := range c.Progress() {
		if ls.Remaining != 0 || ls.CumulativeRefunded != ls.Refundable {
			t.Fatalf("line %s not fully refunded: %+v", ls.LineID, ls)
		}
	}
}

// 分摊依据可审计：每一分钱都能解释来源。
func TestBasisAuditable(t *testing.T) {
	c := mustCalc(t, sampleOrder())
	res := mustApply(t, c, Request{RequestID: "a1", Lines: []LineRefund{
		{LineID: "item-a", Qty: 2},
		{LineID: "ship", Amount: 60},
	}})
	for _, lr := range res.Lines {
		if lr.Basis == "" {
			t.Fatalf("line %s missing basis", lr.LineID)
		}
		if lr.ThisRefund != lr.CumulativeRefunded-lr.PreviouslyRefunded {
			t.Fatalf("line %s: this != cumulative - previous", lr.LineID)
		}
		if lr.Remaining != lr.Refundable-lr.CumulativeRefunded {
			t.Fatalf("line %s: remaining mismatch", lr.LineID)
		}
	}
	wantItem := "floor(1000*2/3) - floor(1000*0/3) = 666 - 0 = 666"
	if res.Lines[0].Basis != wantItem {
		t.Fatalf("item basis = %q, want %q", res.Lines[0].Basis, wantItem)
	}
	wantShip := "min(0 + 60, refundable 150) = 60"
	if res.Lines[1].Basis != wantShip {
		t.Fatalf("ship basis = %q, want %q", res.Lines[1].Basis, wantShip)
	}
}

// 订单校验。
func TestOrderValidation(t *testing.T) {
	bad := []Order{
		{ID: "", Lines: []Line{{ID: "a", Kind: LineKindItem, Qty: 1, Paid: 1}}},
		{ID: "o", Lines: nil},
		{ID: "o", Lines: []Line{{ID: "a", Kind: LineKindItem, Qty: 1, Paid: 1}, {ID: "a", Kind: LineKindItem, Qty: 1, Paid: 1}}},
		{ID: "o", Lines: []Line{{ID: "a", Kind: LineKindItem, Qty: 0, Paid: 1}}},
		{ID: "o", Lines: []Line{{ID: "a", Kind: LineKindItem, Qty: 1, Paid: 10, Discount: 11}}},
		{ID: "o", Lines: []Line{{ID: "a", Kind: LineKindItem, Qty: 1, Paid: -1}}},
	}
	for i, o := range bad {
		if _, err := NewCalculator(o); err == nil {
			t.Fatalf("bad order %d should fail validation", i)
		}
	}
}
