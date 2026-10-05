// Package refund 提供分批退款计算能力。
//
// 设计目标：
//  1. 订单包含商品行、运费与已分摊优惠，整单可退额在创建时即固定。
//  2. 尾差（除不尽的分）按确定性规则分配到具体“单位序号”，
//     因此分批退款的顺序不会改变任何一件商品的最终可退额。
//  3. 退款请求携带幂等键，重复请求返回首次结果，不重复计费。
//  4. 每一笔分摊都输出组成明细（商品价、优惠扣回、尾差），
//     保证每一分钱都有据可查。
package refund

import (
	"errors"
	"fmt"
	"sort"
)

// Line 订单中的一个商品行。DiscountCents 为已分摊到该行的优惠总额（非负）。
type Line struct {
	ID             string
	UnitPriceCents int64
	Qty            int
	DiscountCents  int64
}

// Order 一笔可退款的订单。
type Order struct {
	Lines                 []Line
	ShippingCents         int64
	ShippingDiscountCents int64
}

// ItemRefund 请求中针对某一商品行的退货数量。
type ItemRefund struct {
	LineID string
	Qty    int
}

// Request 一次分批退款请求。Key 为幂等键：相同 Key 的重复请求
// 不会重复计费，而是返回首次请求的结果。
type Request struct {
	Key            string
	Items          []ItemRefund
	RefundShipping bool
}

// Component 一笔分摊的组成成分，说明每一分钱的来源。
type Component struct {
	Kind   string // "unit_price" | "discount_clawback" | "remainder" | "shipping" | "shipping_discount_clawback"
	Cents  int64  // 优惠扣回为负数
	Reason string // 人类可读的分摊依据
}

// Allocation 一次请求中落到某个退款对象（商品行或运费）上的金额。
type Allocation struct {
	LineID     string // 运费为 "shipping"
	Qty        int    // 商品行退的件数；运费为 0
	UnitIndex  []int  // 本次退到的商品单位序号（尾差按序号确定）
	TotalCents int64
	Components []Component
}

// Result 一次退款请求的结果。
type Result struct {
	Key             string
	Duplicate       bool // 是否为重复请求（未重复计费）
	TotalCents      int64
	Allocations     []Allocation
	CumulativeCents int64 // 含本次在内的累计已退
	RemainingCents  int64 // 整单剩余可退
}

type lineState struct {
	line        Line
	refundable  int64 // 该行总可退额 = 单价*数量 - 已分摊优惠
	perUnitBase int64 // 每件基础可退 = refundable / Qty（向下取整）
	remainderN  int   // 前 remainderN 个单位序号每件多分 1 分
	refundedQty int
}

// Session 一笔订单的退款会话，持有全部退款进度与幂等记录。
type Session struct {
	lines            []*lineState
	byID             map[string]*lineState
	shippingGross    int64
	shippingDiscount int64
	shippingRefund   int64
	shippingRefunded bool
	processed        map[string]*Result
	cumulative       int64
	total            int64
}

// NewSession 校验订单并创建退款会话。整单可退额在此刻固定。
func NewSession(o Order) (*Session, error) {
	if len(o.Lines) == 0 && o.ShippingCents == 0 {
		return nil, errors.New("refund: empty order")
	}
	s := &Session{
		byID:      make(map[string]*lineState, len(o.Lines)),
		processed: make(map[string]*Result),
	}
	for _, ln := range o.Lines {
		if ln.ID == "" {
			return nil, errors.New("refund: line with empty ID")
		}
		if _, dup := s.byID[ln.ID]; dup {
			return nil, fmt.Errorf("refund: duplicate line ID %q", ln.ID)
		}
		if ln.Qty <= 0 {
			return nil, fmt.Errorf("refund: line %q: qty must be positive", ln.ID)
		}
		if ln.UnitPriceCents < 0 || ln.DiscountCents < 0 {
			return nil, fmt.Errorf("refund: line %q: negative price or discount", ln.ID)
		}
		gross := ln.UnitPriceCents * int64(ln.Qty)
		if ln.DiscountCents > gross {
			return nil, fmt.Errorf("refund: line %q: discount %d exceeds gross %d", ln.ID, ln.DiscountCents, gross)
		}
		refundable := gross - ln.DiscountCents
		st := &lineState{
			line:        ln,
			refundable:  refundable,
			perUnitBase: refundable / int64(ln.Qty),
			remainderN:  int(refundable % int64(ln.Qty)),
		}
		s.lines = append(s.lines, st)
		s.byID[ln.ID] = st
		s.total += refundable
	}
	if o.ShippingCents < 0 || o.ShippingDiscountCents < 0 {
		return nil, errors.New("refund: negative shipping or shipping discount")
	}
	if o.ShippingDiscountCents > o.ShippingCents {
		return nil, fmt.Errorf("refund: shipping discount %d exceeds shipping %d", o.ShippingDiscountCents, o.ShippingCents)
	}
	s.shippingGross = o.ShippingCents
	s.shippingDiscount = o.ShippingDiscountCents
	s.shippingRefund = o.ShippingCents - o.ShippingDiscountCents
	s.total += s.shippingRefund
	return s, nil
}

// TotalRefundable 返回整单可退额（商品 + 运费 - 全部已分摊优惠）。
func (s *Session) TotalRefundable() int64 { return s.total }

// RemainingRefundable 返回剩余可退额。
func (s *Session) RemainingRefundable() int64 { return s.total - s.cumulative }

// unitAmount 返回某一行第 idx 个单位（0 起）的可退额。
// 尾差规则：行可退额对件数取余，余数 R 表示序号为 0..R-1 的
// 单位每件多分 1 分。规则只依赖单位序号，与退款先后顺序无关。
func (st *lineState) unitAmount(idx int) int64 {
	amt := st.perUnitBase
	if idx < st.remainderN {
		amt++
	}
	return amt
}

// Refund 处理一次分批退款请求。
func (s *Session) Refund(req Request) (*Result, error) {
	if req.Key == "" {
		return nil, errors.New("refund: request key (idempotency key) is required")
	}
	if prev, ok := s.processed[req.Key]; ok {
		dup := *prev
		dup.Duplicate = true
		return &dup, nil
	}

	res := &Result{Key: req.Key}

	// 先校验全部请求，再统一入账，避免部分成功。
	type pendingLine struct {
		st  *lineState
		qty int
	}
	var pending []pendingLine
	seen := make(map[string]bool)
	for _, ir := range req.Items {
		if ir.Qty <= 0 {
			return nil, fmt.Errorf("refund: line %q: refund qty must be positive", ir.LineID)
		}
		if seen[ir.LineID] {
			return nil, fmt.Errorf("refund: line %q: repeated in one request", ir.LineID)
		}
		seen[ir.LineID] = true
		st, ok := s.byID[ir.LineID]
		if !ok {
			return nil, fmt.Errorf("refund: unknown line %q", ir.LineID)
		}
		if st.refundedQty+ir.Qty > st.line.Qty {
			return nil, fmt.Errorf("refund: line %q: refund qty %d exceeds remaining %d",
				ir.LineID, ir.Qty, st.line.Qty-st.refundedQty)
		}
		pending = append(pending, pendingLine{st: st, qty: ir.Qty})
	}
	if req.RefundShipping && s.shippingRefunded {
		return nil, errors.New("refund: shipping already refunded")
	}
	if len(pending) == 0 && !req.RefundShipping {
		return nil, errors.New("refund: empty refund request")
	}

	// 入账：按行 ID 排序处理，保证同一请求内的分摊输出顺序稳定。
	sort.Slice(pending, func(i, j int) bool { return pending[i].st.line.ID < pending[j].st.line.ID })
	for _, p := range pending {
		st := p.st
		alloc := Allocation{LineID: st.line.ID, Qty: p.qty}
		var gross, clawback, remainder int64
		for i := 0; i < p.qty; i++ {
			idx := st.refundedQty + i
			alloc.UnitIndex = append(alloc.UnitIndex, idx)
			amt := st.unitAmount(idx)
			alloc.TotalCents += amt
			// 每件的组成：单价份额 + 优惠扣回份额 + 尾差。
			// 用“行总额按比例”反推每件的名义单价与优惠份额，
			// 使三个成分之和恰等于该件可退额。
			unitGross := st.line.UnitPriceCents
			unitClaw := st.perUnitBase - st.line.UnitPriceCents // <= 0
			unitRem := amt - st.perUnitBase
			gross += unitGross
			clawback += unitClaw
			remainder += unitRem
		}
		alloc.Components = append(alloc.Components, Component{
			Kind:   "unit_price",
			Cents:  gross,
			Reason: fmt.Sprintf("商品价 %d 分/件 × %d 件", st.line.UnitPriceCents, p.qty),
		})
		if clawback != 0 {
			alloc.Components = append(alloc.Components, Component{
				Kind:  "discount_clawback",
				Cents: clawback,
				Reason: fmt.Sprintf("扣回该行已分摊优惠：行优惠 %d 分按 %d 件均摊，每件 %d 分",
					st.line.DiscountCents, st.line.Qty, -st.perUnitBase+st.line.UnitPriceCents),
			})
		}
		if remainder != 0 {
			alloc.Components = append(alloc.Components, Component{
				Kind:  "remainder",
				Cents: remainder,
				Reason: fmt.Sprintf("尾差：行可退 %d 分 ÷ %d 件余 %d 分，按单位序号 0..%d 每件补 1 分，本次命中 %d 分",
					st.refundable, st.line.Qty, st.remainderN, st.remainderN-1, remainder),
			})
		}
		st.refundedQty += p.qty
		res.Allocations = append(res.Allocations, alloc)
		res.TotalCents += alloc.TotalCents
	}

	if req.RefundShipping {
		alloc := Allocation{LineID: "shipping", TotalCents: s.shippingRefund}
		alloc.Components = append(alloc.Components, Component{
			Kind:   "shipping",
			Cents:  s.shippingRefund,
			Reason: fmt.Sprintf("运费 %d 分 - 运费优惠 %d 分，一次性退还", s.shippingGross, s.shippingDiscount),
		})
		s.shippingRefunded = true
		res.Allocations = append(res.Allocations, alloc)
		res.TotalCents += alloc.TotalCents
	}

	s.cumulative += res.TotalCents
	res.CumulativeCents = s.cumulative
	res.RemainingCents = s.total - s.cumulative
	s.processed[req.Key] = res
	return res, nil
}
