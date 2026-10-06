// Package cutstock solves the one-dimensional cutting stock problem.
//
// Stock bars come in several integer lengths, each with a purchase price.
// Every saw cut consumes kerf k of material. Parts come from at most
// MaxOrders orders; parts of equal length from different orders are kept
// distinct. The last piece placed on a bar needs no trailing cut, so a bar
// holding n pieces loses (n-1)*k to the saw.
//
// Solve minimizes total purchase cost first, then total leftover waste.
// Verify independently re-checks a solution by subset grouping enumeration.
package cutstock

import (
	"errors"
	"fmt"
	"strings"
)

// MaxOrders is the maximum number of distinct part orders supported.
const MaxOrders = 12

// Stock is a purchasable bar of raw material.
type Stock struct {
	Length int
	Price  int
}

// Order is a demand for Qty parts of the given Length.
type Order struct {
	Length int
	Qty    int
}

// Piece is one part placed on a bar. Order indexes into the Orders slice.
type Piece struct {
	Order  int
	Length int
}

// Bar is the layout of one physical stock bar.
type Bar struct {
	Stock    int     // index into the Stocks slice
	Length   int     // stock length
	Price    int     // stock price
	Pieces   []Piece // parts in cutting order
	Cuts     int     // saw cuts made on this bar (len(Pieces)-1)
	Leftover int     // remaining waste length
}

// Solution is the result of Solve.
type Solution struct {
	Bars       []Bar
	TotalCost  int // sum of purchased stock prices
	TotalWaste int // sum of bar leftovers
	TotalKerf  int // total length lost to saw cuts
}

// vec is a remaining-demand vector, one slot per order.
type vec [MaxOrders]int

// pattern is a multiset of parts cut from a single bar, already bound to
// the cheapest (then shortest) stock that can hold it.
type pattern struct {
	cnt      vec
	stock    int
	price    int
	leftover int
}

type memoEntry struct {
	cost, waste int
	pat         pattern
	ok          bool
}

type solver struct {
	kerf     int
	stocks   []Stock
	orders   []Order
	m        int
	patterns []pattern
	memo     map[vec]memoEntry
}

// Solve finds the exact optimal cutting plan: minimum total purchase cost,
// and among those, minimum total leftover waste.
func Solve(stocks []Stock, orders []Order, kerf int) (*Solution, error) {
	if err := validate(stocks, orders, kerf); err != nil {
		return nil, err
	}
	s := &solver{kerf: kerf, stocks: stocks, orders: orders, m: len(orders)}
	s.buildPatterns()

	var demand vec
	total := 0
	for i, o := range orders {
		demand[i] = o.Qty
		total += o.Qty
	}
	if total == 0 {
		return &Solution{}, nil
	}

	s.memo = make(map[vec]memoEntry)
	best := s.f(demand)
	if !best.ok {
		return nil, errors.New("cutstock: no feasible plan")
	}

	sol := &Solution{}
	rem := demand
	for rem != (vec{}) {
		e := s.memo[rem]
		bar := Bar{
			Stock:    e.pat.stock,
			Length:   stocks[e.pat.stock].Length,
			Price:    stocks[e.pat.stock].Price,
			Leftover: e.pat.leftover,
		}
		for i := 0; i < s.m; i++ {
			for c := 0; c < e.pat.cnt[i]; c++ {
				bar.Pieces = append(bar.Pieces, Piece{Order: i, Length: orders[i].Length})
				rem[i]--
			}
		}
		bar.Cuts = len(bar.Pieces) - 1
		sol.TotalKerf += bar.Cuts * kerf
		sol.TotalWaste += bar.Leftover
		sol.TotalCost += bar.Price
		sol.Bars = append(sol.Bars, bar)
	}
	return sol, nil
}

func validate(stocks []Stock, orders []Order, kerf int) error {
	if kerf < 0 {
		return errors.New("cutstock: negative kerf")
	}
	if len(orders) > MaxOrders {
		return fmt.Errorf("cutstock: %d orders exceeds MaxOrders=%d", len(orders), MaxOrders)
	}
	if len(stocks) == 0 {
		return errors.New("cutstock: no stock types")
	}
	maxLen := 0
	for _, st := range stocks {
		if st.Length <= 0 || st.Price < 0 {
			return fmt.Errorf("cutstock: invalid stock %+v", st)
		}
		if st.Length > maxLen {
			maxLen = st.Length
		}
	}
	for _, o := range orders {
		if o.Length <= 0 || o.Qty < 0 {
			return fmt.Errorf("cutstock: invalid order %+v", o)
		}
		if o.Qty > 0 && o.Length > maxLen {
			return fmt.Errorf("cutstock: part length %d fits no stock", o.Length)
		}
	}
	return nil
}

// buildPatterns enumerates every nonempty multiset of demanded parts that
// fits on some stock bar, remembering the best stock for each.
func (s *solver) buildPatterns() {
	maxLen := 0
	for _, st := range s.stocks {
		if st.Length > maxLen {
			maxLen = st.Length
		}
	}
	var cnt vec
	var rec func(i, sum, pieces int)
	rec = func(i, sum, pieces int) {
		if i == s.m {
			if pieces == 0 {
				return
			}
			need := sum + (pieces-1)*s.kerf
			stock := s.bestStock(need)
			if stock < 0 {
				return
			}
			s.patterns = append(s.patterns, pattern{
				cnt:      cnt,
				stock:    stock,
				price:    s.stocks[stock].Price,
				leftover: s.stocks[stock].Length - need,
			})
			return
		}
		for c := 0; c <= s.orders[i].Qty; c++ {
			nsum := sum + c*s.orders[i].Length
			npieces := pieces + c
			// Prune: a partial load that already exceeds the longest
			// stock can never become feasible by adding more parts.
			if npieces > 0 && nsum+(npieces-1)*s.kerf > maxLen {
				break
			}
			cnt[i] = c
			rec(i+1, nsum, npieces)
		}
		cnt[i] = 0
	}
	rec(0, 0, 0)
}

// bestStock returns the index of the cheapest stock with Length >= need,
// breaking price ties toward the shorter (less wasteful) bar.
func (s *solver) bestStock(need int) int {
	best := -1
	for i, st := range s.stocks {
		if st.Length < need {
			continue
		}
		if best < 0 || st.Price < s.stocks[best].Price ||
			(st.Price == s.stocks[best].Price && st.Length < s.stocks[best].Length) {
			best = i
		}
	}
	return best
}

// f returns the optimal (cost, waste) plan for the remaining demand.
func (s *solver) f(rem vec) memoEntry {
	if rem == (vec{}) {
		return memoEntry{ok: true}
	}
	if e, hit := s.memo[rem]; hit {
		return e
	}
	var best memoEntry
	for _, p := range s.patterns {
		var next vec
		fits := true
		for i := 0; i < s.m; i++ {
			if p.cnt[i] > rem[i] {
				fits = false
				break
			}
			next[i] = rem[i] - p.cnt[i]
		}
		if !fits {
			continue
		}
		sub := s.f(next)
		if !sub.ok {
			continue
		}
		cost := p.price + sub.cost
		waste := p.leftover + sub.waste
		if !best.ok || cost < best.cost || (cost == best.cost && waste < best.waste) {
			best = memoEntry{cost: cost, waste: waste, pat: p, ok: true}
		}
	}
	s.memo[rem] = best
	return best
}

// Layout renders the per-bar cutting plan.
func (s *Solution) Layout() string {
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 根母材, 采购成本 %d, 总余料 %d, 总锯缝损耗 %d\n",
		len(s.Bars), s.TotalCost, s.TotalWaste, s.TotalKerf)
	for i, bar := range s.Bars {
		fmt.Fprintf(&b, "第%d根: 母材#%d 长=%d 价=%d |", i+1, bar.Stock, bar.Length, bar.Price)
		for _, p := range bar.Pieces {
			fmt.Fprintf(&b, " 订单#%d长%d", p.Order, p.Length)
		}
		fmt.Fprintf(&b, " | 刀数=%d 余料=%d\n", bar.Cuts, bar.Leftover)
	}
	return b.String()
}

// Verify independently checks a solution by subset grouping enumeration:
// pieces are grouped by bar (each subset must fit its stock) and by order
// (each group must exactly cover the demand), and all cut counts,
// leftovers and totals are recomputed from scratch.
func Verify(stocks []Stock, orders []Order, kerf int, sol *Solution) error {
	if sol == nil {
		return errors.New("cutstock: nil solution")
	}
	if len(orders) > MaxOrders {
		return fmt.Errorf("cutstock: %d orders exceeds MaxOrders=%d", len(orders), MaxOrders)
	}
	covered := make([]int, len(orders))
	cost, waste, kerfLoss := 0, 0, 0
	for bi, bar := range sol.Bars {
		if len(bar.Pieces) == 0 {
			return fmt.Errorf("cutstock: bar %d is empty", bi)
		}
		if bar.Stock < 0 || bar.Stock >= len(stocks) {
			return fmt.Errorf("cutstock: bar %d has invalid stock index %d", bi, bar.Stock)
		}
		st := stocks[bar.Stock]
		if bar.Length != st.Length || bar.Price != st.Price {
			return fmt.Errorf("cutstock: bar %d stock mismatch", bi)
		}
		sum := 0
		for _, p := range bar.Pieces {
			if p.Order < 0 || p.Order >= len(orders) {
				return fmt.Errorf("cutstock: bar %d has invalid order index %d", bi, p.Order)
			}
			if p.Length != orders[p.Order].Length {
				return fmt.Errorf("cutstock: bar %d piece length %d != order %d length %d",
					bi, p.Length, p.Order, orders[p.Order].Length)
			}
			sum += p.Length
			covered[p.Order]++
		}
		// The last piece on a bar needs no trailing cut.
		cuts := len(bar.Pieces) - 1
		if cuts != bar.Cuts {
			return fmt.Errorf("cutstock: bar %d cuts %d, want %d", bi, bar.Cuts, cuts)
		}
		used := sum + cuts*kerf
		if used > st.Length {
			return fmt.Errorf("cutstock: bar %d overflows: used %d > length %d", bi, used, st.Length)
		}
		if left := st.Length - used; left != bar.Leftover {
			return fmt.Errorf("cutstock: bar %d leftover %d, want %d", bi, bar.Leftover, left)
		}
		cost += st.Price
		waste += bar.Leftover
		kerfLoss += cuts * kerf
	}
	for i, o := range orders {
		if covered[i] != o.Qty {
			return fmt.Errorf("cutstock: order %d covered %d parts, want %d", i, covered[i], o.Qty)
		}
	}
	if cost != sol.TotalCost {
		return fmt.Errorf("cutstock: total cost %d, want %d", sol.TotalCost, cost)
	}
	if waste != sol.TotalWaste {
		return fmt.Errorf("cutstock: total waste %d, want %d", sol.TotalWaste, waste)
	}
	if kerfLoss != sol.TotalKerf {
		return fmt.Errorf("cutstock: total kerf %d, want %d", sol.TotalKerf, kerfLoss)
	}
	return nil
}
