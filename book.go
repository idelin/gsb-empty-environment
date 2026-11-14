package netsec

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
	"time"
)

// Clock supplies engine-controlled time. All surveillance windows use this
// clock so clients cannot influence them by declaring timestamps.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// DefaultClock returns a clock backed by time.Now.
func DefaultClock() Clock { return wallClock{} }

// ErrOrderNotFound is returned by Cancel for unknown or non-live orders.
var ErrOrderNotFound = errors.New("netsec: order not found")

// OrderBook is a concurrency-safe price-time priority order book for one
// symbol. Sorting uses only price and the engine-assigned monotonic sequence.
type OrderBook struct {
	mu      sync.Mutex
	symbol  string
	clock   Clock
	monitor *Monitor
	nextSeq uint64
	orders  map[string]*Order
	index   map[*Order]*queuedOrder
	buys    *sideBook
	sells   *sideBook
}

// BookOption configures a new OrderBook.
type BookOption func(*OrderBook)

// WithClock injects an engine-controlled clock (used by Monitor callbacks).
func WithClock(c Clock) BookOption {
	return func(b *OrderBook) {
		if c != nil {
			b.clock = c
		}
	}
}

// WithMonitor attaches a surveillance monitor. Submit enforces its frequency
// and self-trade rules; Cancel records cancel frequency through it.
func WithMonitor(m *Monitor) BookOption {
	return func(b *OrderBook) { b.monitor = m }
}

// NewOrderBook creates an empty book bound to symbol.
func NewOrderBook(symbol string, opts ...BookOption) *OrderBook {
	b := &OrderBook{
		symbol:  symbol,
		clock:   DefaultClock(),
		nextSeq: 1,
		orders:  make(map[string]*Order),
		index:   make(map[*Order]*queuedOrder),
		buys:    newSideBook(SideBuy),
		sells:   newSideBook(SideSell),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Symbol returns the instrument this book serves.
func (b *OrderBook) Symbol() string { return b.symbol }

// NextSequence reports the sequence the next accepted order will receive. It
// never decreases for the lifetime of the book.
func (b *OrderBook) NextSequence() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextSeq
}

// Submit validates and admits an order. On success the order receives the
// next engine sequence and is appended to the tail of its price level.
//
// With a Monitor attached, frequency and self-trade prevention rules run
// before admission; a rejected request consumes no sequence and rests nowhere.
func (b *OrderBook) Submit(req OrderRequest) (*Order, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.orders[req.OrderID]; exists {
		return nil, ErrDuplicateOrderID
	}

	now := b.clock.Now()
	if b.monitor != nil {
		if err := b.monitor.CheckSubmission(req.Owner, now); err != nil {
			return nil, err
		}
		if taker := b.firstMatchable(req); taker != nil &&
			b.monitor.SameOwner(req.Owner, taker.Owner()) {
			b.monitor.RecordViolation(ViolationSelfTrade, req.Owner, taker.OrderID(), now)
			return nil, ErrSelfTrade
		}
	}

	order := &Order{
		Sequence: b.nextSeq,
		Status:   StatusLive,
		req:      req,
	}
	b.nextSeq++
	b.orders[req.OrderID] = order

	target := b.buys
	if req.Side == SideSell {
		target = b.sells
	}
	target.push(order)
	b.index[order] = target.levels[order.Price()].tail
	return order, nil
}

// Cancel removes a live order from its price-level queue. A later resubmission
// of the same economic order is a fresh Submit and therefore gets a new
// sequence, landing at the tail of the level.
func (b *OrderBook) Cancel(orderID string) (*Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	order, ok := b.orders[orderID]
	if !ok || order.Status != StatusLive {
		return nil, ErrOrderNotFound
	}

	target := b.buys
	if order.Side() == SideSell {
		target = b.sells
	}
	target.remove(order, b.index)
	delete(b.index, order)
	delete(b.orders, orderID)
	order.Status = StatusCancelled

	if b.monitor != nil {
		b.monitor.RecordCancel(order.Owner(), b.clock.Now())
	}
	return order, nil
}

// Pop removes and returns the highest-priority resting order on side: the
// best price (highest bid or lowest ask), then the lowest sequence at that
// price. It returns nil when the side is empty.
func (b *OrderBook) Pop(side Side) *Order {
	b.mu.Lock()
	defer b.mu.Unlock()

	target := b.bookFor(side)
	if target == nil || target.Len() == 0 {
		return nil
	}
	order := target.pop()
	delete(b.index, order)
	delete(b.orders, order.OrderID())
	order.Status = StatusFilled
	return order
}

// BestPrice returns the best price for side (max bid, min ask) and false when
// the side is empty.
func (b *OrderBook) BestPrice(side Side) (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.bookFor(side)
	if target == nil || target.Len() == 0 {
		return 0, false
	}
	return target.bestPrice(), true
}

// Order returns the live order with id, or nil if it is not resting.
func (b *OrderBook) Order(orderID string) *Order {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.orders[orderID]
}

// Len returns the number of live resting orders on a side.
func (b *OrderBook) Len(side Side) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.bookFor(side)
	if target == nil {
		return 0
	}
	return target.Len()
}

// Queue returns the orders resting at price on side in exact priority order.
func (b *OrderBook) Queue(side Side, price int64) []*Order {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.bookFor(side)
	if target == nil {
		return nil
	}
	return target.queueAt(price)
}

// Snapshot returns all live orders on side in matching priority order.
func (b *OrderBook) Snapshot(side Side) []*Order {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.bookFor(side)
	if target == nil {
		return nil
	}

	prices := make([]int64, 0, len(target.levels))
	for price := range target.levels {
		prices = append(prices, price)
	}
	sort.Slice(prices, func(i, j int) bool {
		if side == SideBuy {
			return prices[i] > prices[j]
		}
		return prices[i] < prices[j]
	})

	var out []*Order
	for _, price := range prices {
		out = append(out, target.queueAt(price)...)
	}
	return out
}

func (b *OrderBook) bookFor(side Side) *sideBook {
	switch side {
	case SideBuy:
		return b.buys
	case SideSell:
		return b.sells
	default:
		return nil
	}
}

// firstMatchable returns the first resting order an incoming req would match
// against under price-time priority, or nil when nothing is matchable.
// Caller must hold b.mu.
func (b *OrderBook) firstMatchable(req OrderRequest) *Order {
	opposite := b.sells
	if req.Side == SideSell {
		opposite = b.buys
	}
	if opposite.Len() == 0 {
		return nil
	}
	best := opposite.bestPrice()
	if req.Side == SideBuy && best > req.Price {
		return nil
	}
	if req.Side == SideSell && best < req.Price {
		return nil
	}
	return opposite.peek()
}

// queuedOrder is a node in the FIFO list of one price level.
type queuedOrder struct {
	order *Order
	prev  *queuedOrder
	next  *queuedOrder
}

// priceLevel is a FIFO queue keyed by engine sequence at one price.
type priceLevel struct {
	price int64
	head  *queuedOrder
	tail  *queuedOrder
	size  int
}

func (l *priceLevel) enqueue(node *queuedOrder) {
	node.prev = l.tail
	node.next = nil
	if l.tail != nil {
		l.tail.next = node
	} else {
		l.head = node
	}
	l.tail = node
	l.size++
}

func (l *priceLevel) remove(node *queuedOrder) {
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		l.head = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		l.tail = node.prev
	}
	node.prev = nil
	node.next = nil
	l.size--
}

func (l *priceLevel) dequeue() *queuedOrder {
	node := l.head
	if node == nil {
		return nil
	}
	l.remove(node)
	return node
}

func (l *priceLevel) orders() []*Order {
	out := make([]*Order, 0, l.size)
	for node := l.head; node != nil; node = node.next {
		out = append(out, node.order)
	}
	return out
}

// sideBook holds price levels ordered through a heap plus per-level FIFO lists.
type sideBook struct {
	side   Side
	levels map[int64]*priceLevel
	heap   priceHeap
}

func newSideBook(side Side) *sideBook {
	return &sideBook{
		side:   side,
		levels: make(map[int64]*priceLevel),
		heap:   priceHeap{side: side},
	}
}

func (s *sideBook) Len() int { return s.heap.Len() }

func (s *sideBook) push(order *Order) {
	level, ok := s.levels[order.Price()]
	if !ok {
		level = &priceLevel{price: order.Price()}
		s.levels[order.Price()] = level
		heap.Push(&s.heap, level)
	}
	node := &queuedOrder{order: order}
	level.enqueue(node)
}

func (s *sideBook) remove(order *Order, index map[*Order]*queuedOrder) {
	level, ok := s.levels[order.Price()]
	if !ok {
		return
	}
	node, ok := index[order]
	if !ok {
		return
	}
	level.remove(node)
	if level.size == 0 {
		s.dropLevel(level)
	}
}

func (s *sideBook) pop() *Order {
	level := s.heap.data[0]
	node := level.dequeue()
	if level.size == 0 {
		s.dropLevel(level)
	}
	return node.order
}

func (s *sideBook) peek() *Order {
	if s.heap.Len() == 0 {
		return nil
	}
	return s.heap.data[0].head.order
}

func (s *sideBook) bestPrice() int64 { return s.heap.data[0].price }

func (s *sideBook) queueAt(price int64) []*Order {
	level, ok := s.levels[price]
	if !ok {
		return nil
	}
	return level.orders()
}

func (s *sideBook) dropLevel(level *priceLevel) {
	for i, candidate := range s.heap.data {
		if candidate == level {
			heap.Remove(&s.heap, i)
			break
		}
	}
	delete(s.levels, level.price)
}

// priceHeap orders levels by best price: max-heap for bids, min-heap for asks.
type priceHeap struct {
	side Side
	data []*priceLevel
}

func (h priceHeap) Len() int { return len(h.data) }

func (h priceHeap) Less(i, j int) bool {
	if h.side == SideBuy {
		return h.data[i].price > h.data[j].price
	}
	return h.data[i].price < h.data[j].price
}

func (h priceHeap) Swap(i, j int) {
	h.data[i], h.data[j] = h.data[j], h.data[i]
}

func (h *priceHeap) Push(x any) { h.data = append(h.data, x.(*priceLevel)) }

func (h *priceHeap) Pop() any {
	last := len(h.data) - 1
	level := h.data[last]
	h.data = h.data[:last]
	return level
}
