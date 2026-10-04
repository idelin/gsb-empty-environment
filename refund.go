// Package refund 提供分批退款计算能力。
//
// 设计原则：
//  1. 每行的"累计已退额"是"累计已退数量/金额"的纯函数，与批次划分和退款顺序无关。
//  2. 商品行按数量退款：累计已退 q 件时的累计退款额 = floor(可退额 * q / 总数量)，
//     尾差（除不尽的零头）确定性地落在最后几件上，任何退款顺序下每件商品的
//     最终可退额完全一致。
//  3. 运费行按金额退款：累计已退 = min(历史累计 + 本次请求, 可退额)。
//  4. 幂等：同一 RequestID 重复提交返回首次计算结果，不重复计费；
//     同一 RequestID 携带不同内容视为冲突，返回错误。
//  5. 每一分钱的分摊依据通过 LineResult.Basis 输出，可审计。
package refund

import (
	"errors"
	"fmt"
	"sort"
)

// Money 金额，单位：分。
type Money int64

// LineKind 订单行类型。
type LineKind int

const (
	// LineKindItem 商品行，按数量退款。
	LineKindItem LineKind = iota
	// LineKindShipping 运费行，按金额退款。
	LineKindShipping
)

func (k LineKind) String() string {
	if k == LineKindShipping {
		return "shipping"
	}
	return "item"
}

// Line 订单行：一件商品（可含多件）或一笔运费。
type Line struct {
	ID       string // 行唯一标识
	Kind     LineKind
	Qty      int64 // 商品数量（仅商品行有效，必须 > 0）
	Paid     Money // 该实付金额（分摊优惠前）
	Discount Money // 已分摊到该行的优惠金额
}

// Refundable 该行可退总额 = 实付 - 已分摊优惠。
func (l Line) Refundable() Money { return l.Paid - l.Discount }

// Order 一笔订单。
type Order struct {
	ID    string
	Lines []Line
}

// Validate 校验订单合法性。
func (o Order) Validate() error {
	if o.ID == "" {
		return errors.New("refund: order id is empty")
	}
	if len(o.Lines) == 0 {
		return errors.New("refund: order has no lines")
	}
	seen := make(map[string]bool, len(o.Lines))
	for _, l := range o.Lines {
		if l.ID == "" {
			return errors.New("refund: line id is empty")
		}
		if seen[l.ID] {
			return fmt.Errorf("refund: duplicate line id %q", l.ID)
		}
		seen[l.ID] = true
		if l.Paid < 0 || l.Discount < 0 {
			return fmt.Errorf("refund: line %q has negative amount", l.ID)
		}
		if l.Discount > l.Paid {
			return fmt.Errorf("refund: line %q discount %d exceeds paid %d", l.ID, l.Discount, l.Paid)
		}
		switch l.Kind {
		case LineKindItem:
			if l.Qty <= 0 {
				return fmt.Errorf("refund: item line %q qty must be positive", l.ID)
			}
		case LineKindShipping:
			if l.Qty != 0 {
				return fmt.Errorf("refund: shipping line %q must have qty 0", l.ID)
			}
		default:
			return fmt.Errorf("refund: line %q has unknown kind", l.ID)
		}
	}
	return nil
}

// TotalRefundable 整单可退总额。
func (o Order) TotalRefundable() Money {
	var total Money
	for _, l := range o.Lines {
		total += l.Refundable()
	}
	return total
}

// LineRefund 一次退款请求中针对某一行的退款意图。
type LineRefund struct {
	LineID string
	Qty    int64 // 商品行：本次退几件（必须 > 0）
	Amount Money // 运费行：本次请求退多少分（必须 > 0）
}

// Request 一次分批退款请求。RequestID 用于幂等去重。
type Request struct {
	RequestID string
	Lines     []LineRefund
}

// LineResult 一行在一次退款中的分摊结果与依据。
type LineResult struct {
	LineID             string
	Kind               LineKind
	Refundable         Money  // 该行可退总额
	PreviouslyRefunded Money  // 本次之前累计已退
	ThisRefund         Money  // 本次退款额
	CumulativeRefunded Money  // 本次之后累计已退
	Remaining          Money  // 剩余可退
	CumulativeQty      int64  // 商品行：本次之后累计已退件数
	Basis              string // 每一分钱的分摊依据（可审计）
}

// Result 一次退款请求的计算结果。
type Result struct {
	RequestID string
	Duplicate bool // 是否为重复请求（命中幂等缓存，未重复计费）
	Lines     []LineResult
	Total     Money // 本次退款总额
}

// lineState 行的累计退款状态。
type lineState struct {
	line           Line
	refundedQty    int64 // 商品行累计已退件数
	refundedAmount Money // 累计已退金额
}

// Calculator 单笔订单的退款计算器，维护累计状态与幂等记录。
// 不是并发安全的；如需并发使用请自行加锁。
type Calculator struct {
	order    Order
	states   map[string]*lineState
	requests map[string]Request // RequestID -> 原始请求（用于冲突检测）
	results  map[string]Result  // RequestID -> 首次计算结果
}

// NewCalculator 创建计算器。
func NewCalculator(o Order) (*Calculator, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	c := &Calculator{
		order:    o,
		states:   make(map[string]*lineState, len(o.Lines)),
		requests: make(map[string]Request),
		results:  make(map[string]Result),
	}
	for _, l := range o.Lines {
		c.states[l.ID] = &lineState{line: l}
	}
	return c, nil
}

// ErrRequestConflict 同一 RequestID 携带了不同的请求内容。
var ErrRequestConflict = errors.New("refund: request id reused with different payload")

// cumulativeItemRefund 商品行累计已退 q 件时的累计退款额。
// 公式 floor(R*q/Q) 保证：
//   - 尾差确定性地落在最后 R%Q 件上；
//   - 结果只取决于累计件数 q，与分批方式无关；
//   - q == Q 时恰好等于 R，整行可退额分文不差。
func cumulativeItemRefund(refundable Money, q, total int64) Money {
	return Money(int64(refundable) * q / total)
}

// Apply 应用一次退款请求，返回分摊结果。
// 重复提交相同 RequestID 与相同内容时，返回首次结果且 Duplicate=true，不重复计费。
func (c *Calculator) Apply(req Request) (Result, error) {
	if req.RequestID == "" {
		return Result{}, errors.New("refund: request id is empty")
	}
	if prev, ok := c.requests[req.RequestID]; ok {
		if !sameRequest(prev, req) {
			return Result{}, ErrRequestConflict
		}
		dup := c.results[req.RequestID]
		dup.Duplicate = true
		return dup, nil
	}
	if len(req.Lines) == 0 {
		return Result{}, errors.New("refund: request has no lines")
	}

	// 先全量校验，再统一生效，避免部分成功。
	type delta struct {
		st      *lineState
		newQty  int64
		newCum  Money
		thisAmt Money
		basis   string
	}
	deltas := make([]delta, 0, len(req.Lines))
	seenLine := make(map[string]bool, len(req.Lines))
	for _, lr := range req.Lines {
		st, ok := c.states[lr.LineID]
		if !ok {
			return Result{}, fmt.Errorf("refund: unknown line id %q", lr.LineID)
		}
		if seenLine[lr.LineID] {
			return Result{}, fmt.Errorf("refund: line %q appears twice in one request", lr.LineID)
		}
		seenLine[lr.LineID] = true

		switch st.line.Kind {
		case LineKindItem:
			if lr.Qty <= 0 {
				return Result{}, fmt.Errorf("refund: line %q refund qty must be positive", lr.LineID)
			}
			if lr.Amount != 0 {
				return Result{}, fmt.Errorf("refund: item line %q must refund by qty, not amount", lr.LineID)
			}
			newQty := st.refundedQty + lr.Qty
			if newQty > st.line.Qty {
				return Result{}, fmt.Errorf("refund: line %q over-refund: cumulative qty %d exceeds %d",
					lr.LineID, newQty, st.line.Qty)
			}
			r := st.line.Refundable()
			newCum := cumulativeItemRefund(r, newQty, st.line.Qty)
			oldCum := st.refundedAmount // 不变式：oldCum == floor(R*refundedQty/Q)
			deltas = append(deltas, delta{
				st: st, newQty: newQty, newCum: newCum, thisAmt: newCum - oldCum,
				basis: fmt.Sprintf("floor(%d*%d/%d) - floor(%d*%d/%d) = %d - %d = %d",
					r, newQty, st.line.Qty, r, st.refundedQty, st.line.Qty, newCum, oldCum, newCum-oldCum),
			})
		case LineKindShipping:
			if lr.Amount <= 0 {
				return Result{}, fmt.Errorf("refund: shipping line %q refund amount must be positive", lr.LineID)
			}
			if lr.Qty != 0 {
				return Result{}, fmt.Errorf("refund: shipping line %q must refund by amount, not qty", lr.LineID)
			}
			r := st.line.Refundable()
			want := st.refundedAmount + lr.Amount
			newCum := want
			if newCum > r {
				newCum = r // 超出部分截断，累计不超过可退额
			}
			deltas = append(deltas, delta{
				st: st, newQty: 0, newCum: newCum, thisAmt: newCum - st.refundedAmount,
				basis: fmt.Sprintf("min(%d + %d, refundable %d) = %d",
					st.refundedAmount, lr.Amount, r, newCum),
			})
		}
	}

	// 生效。
	res := Result{RequestID: req.RequestID}
	for _, d := range deltas {
		prev := d.st.refundedAmount
		d.st.refundedQty = d.newQty
		d.st.refundedAmount = d.newCum
		res.Lines = append(res.Lines, LineResult{
			LineID:             d.st.line.ID,
			Kind:               d.st.line.Kind,
			Refundable:         d.st.line.Refundable(),
			PreviouslyRefunded: prev,
			ThisRefund:         d.thisAmt,
			CumulativeRefunded: d.newCum,
			Remaining:          d.st.line.Refundable() - d.newCum,
			CumulativeQty:      d.newQty,
			Basis:              d.basis,
		})
		res.Total += d.thisAmt
	}

	c.requests[req.RequestID] = req
	c.results[req.RequestID] = res
	return res, nil
}

// LineState 行的累计退款进度快照。
type LineState struct {
	LineID             string
	Kind               LineKind
	Refundable         Money
	RefundedQty        int64
	CumulativeRefunded Money
	Remaining          Money
}

// Progress 返回所有行的累计退款进度，按行 ID 排序，输出确定。
func (c *Calculator) Progress() []LineState {
	out := make([]LineState, 0, len(c.states))
	for _, st := range c.states {
		out = append(out, LineState{
			LineID:             st.line.ID,
			Kind:               st.line.Kind,
			Refundable:         st.line.Refundable(),
			RefundedQty:        st.refundedQty,
			CumulativeRefunded: st.refundedAmount,
			Remaining:          st.line.Refundable() - st.refundedAmount,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LineID < out[j].LineID })
	return out
}

// TotalRefunded 整单累计已退金额。
func (c *Calculator) TotalRefunded() Money {
	var total Money
	for _, st := range c.states {
		total += st.refundedAmount
	}
	return total
}

// FullyRefunded 是否整单已退完（累计已退 == 整单可退额）。
func (c *Calculator) FullyRefunded() bool {
	return c.TotalRefunded() == c.order.TotalRefundable()
}

func sameRequest(a, b Request) bool {
	if a.RequestID != b.RequestID || len(a.Lines) != len(b.Lines) {
		return false
	}
	// 行顺序不同但内容相同视为同一请求。
	am := make(map[string]LineRefund, len(a.Lines))
	for _, l := range a.Lines {
		am[l.LineID] = l
	}
	for _, l := range b.Lines {
		x, ok := am[l.LineID]
		if !ok || x.Qty != l.Qty || x.Amount != l.Amount {
			return false
		}
	}
	return true
}
