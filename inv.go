package main

import (
	"fmt"
	"strings"
)

// Params describes a perishable lot-sizing instance.
// A unit produced on day p is usable on days p .. p+Shelf-1 and
// expires at the end of day p+Shelf-1 (age reaches Shelf).
type Params struct {
	T        int // horizon in days
	Shelf    int // shelf life d in days
	Cap      int // daily production capacity
	Setup    int // fixed setup cost, paid on any day with production > 0
	ProdCost int // per-unit production cost
	HoldCost int // per-unit per-day holding cost on end-of-day inventory
}

const INF = int(^uint(0)>>1) / 4

// StepResult reports what happened while simulating one day.
type StepResult struct {
	OK       bool // false if demand could not be met (stockout)
	Holding  int  // units in stock at end of day (after consumption)
	Expired  int  // units expiring at end of day
	DayCost  int  // setup + production + holding cost of the day
	Produced int
}

// stepDay simulates one day on an age-vector inventory.
// inv has length Shelf-1: inv[i] = units of age i+1 at start of day
// (production of the day gets age 0). Consumption is FEFO: oldest
// (earliest expiring) units are consumed first. Returns the inventory
// at the start of the next day (units that reached age Shelf expire).
func stepDay(p Params, inv []int, produce, demand int) ([]int, StepResult) {
	full := make([]int, p.Shelf)
	full[0] = produce
	copy(full[1:], inv)

	total := produce
	for _, v := range inv {
		total += v
	}
	res := StepResult{Produced: produce}
	if total < demand {
		return nil, res // stockout
	}

	// Consume oldest first (earliest expiry first).
	need := demand
	for age := p.Shelf - 1; age >= 0 && need > 0; age-- {
		take := full[age]
		if take > need {
			take = need
		}
		full[age] -= take
		need -= take
	}

	holding := 0
	for _, v := range full {
		holding += v
	}
	res.Holding = holding
	res.Expired = full[p.Shelf-1]
	res.OK = true
	res.DayCost = p.HoldCost * holding
	if produce > 0 {
		res.DayCost += p.Setup + p.ProdCost*produce
	}

	// Age inventory by one day; units at age Shelf-1 expire.
	next := make([]int, p.Shelf-1)
	for i := 0; i < p.Shelf-1; i++ {
		next[i] = full[i]
	}
	return next, res
}

// invKey encodes an age-vector inventory for memoization.
func invKey(inv []int) string {
	var b strings.Builder
	for i, v := range inv {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", v)
	}
	return b.String()
}

// --- Batch age ledger ---

// LedgerEntry is one line of the batch age account.
type LedgerEntry struct {
	Day      int
	Kind     string // "produce", "consume", "expire", "stock"
	BatchDay int    // production day of the batch (-1 for stock lines)
	Qty      int
	Age      int // age of the batch on this day (-1 for stock lines)
}

func (e LedgerEntry) String() string {
	switch e.Kind {
	case "produce":
		return fmt.Sprintf("day %d: produce  batch(day %d) qty=%d", e.Day, e.BatchDay, e.Qty)
	case "consume":
		return fmt.Sprintf("day %d: consume  batch(day %d) qty=%d age=%d", e.Day, e.BatchDay, e.Qty, e.Age)
	case "expire":
		return fmt.Sprintf("day %d: expire   batch(day %d) qty=%d age=%d (shelf life reached)", e.Day, e.BatchDay, e.Qty, e.Age)
	default:
		return fmt.Sprintf("day %d: stock    on-hand=%d at end of day", e.Day, e.Qty)
	}
}

type batch struct {
	day int
	qty int // remaining
}

// simulatePlan runs a production plan against a demand path with
// batch-level bookkeeping and returns the full age ledger.
// Consumption is FEFO (oldest batch first). ok=false on stockout.
func simulatePlan(p Params, demand, plan []int) (entries []LedgerEntry, cost int, ok bool) {
	batches := []batch{}
	for day := 0; day < p.T; day++ {
		x := 0
		if plan != nil {
			x = plan[day]
		}
		if x > p.Cap {
			return entries, 0, false
		}
		if x > 0 {
			batches = append(batches, batch{day: day, qty: x})
			entries = append(entries, LedgerEntry{Day: day, Kind: "produce", BatchDay: day, Qty: x, Age: 0})
			cost += p.Setup + p.ProdCost*x
		}

		// Consume FEFO: batches are ordered by production day.
		need := demand[day]
		for need > 0 && len(batches) > 0 {
			take := batches[0].qty
			if take > need {
				take = need
			}
			batches[0].qty -= take
			need -= take
			entries = append(entries, LedgerEntry{
				Day: day, Kind: "consume", BatchDay: batches[0].day,
				Qty: take, Age: day - batches[0].day,
			})
			if batches[0].qty == 0 {
				batches = batches[1:]
			}
		}
		if need > 0 {
			return entries, 0, false // stockout
		}

		// Holding cost on end-of-day stock.
		stock := 0
		for _, b := range batches {
			stock += b.qty
		}
		cost += p.HoldCost * stock
		entries = append(entries, LedgerEntry{Day: day, Kind: "stock", BatchDay: -1, Qty: stock, Age: -1})

		// Expire batches whose shelf life ends today.
		kept := batches[:0]
		for _, b := range batches {
			if day-b.day >= p.Shelf-1 {
				entries = append(entries, LedgerEntry{
					Day: day, Kind: "expire", BatchDay: b.day,
					Qty: b.qty, Age: day - b.day + 1,
				})
			} else {
				kept = append(kept, b)
			}
		}
		batches = kept
	}
	return entries, cost, true
}

// checkLedger replays a ledger and validates accounting invariants:
//   - no consumption from a batch older than Shelf-1 days
//   - FEFO order: a newer batch is only touched once every older
//     batch on hand is empty
//   - batch quantities are conserved (never consume/expire more than
//     what remains of a batch)
//   - batches expire exactly when their age reaches Shelf
func checkLedger(p Params, demand []int, entries []LedgerEntry) error {
	remaining := map[int]int{} // batch production day -> qty on hand
	olderEmpty := func(batchDay int) error {
		for d, q := range remaining {
			if d < batchDay && q > 0 {
				return fmt.Errorf("FEFO violated: consumed batch(day %d) while older batch(day %d) still has %d", batchDay, d, q)
			}
		}
		return nil
	}
	for _, e := range entries {
		switch e.Kind {
		case "produce":
			if e.BatchDay != e.Day {
				return fmt.Errorf("day %d: production recorded under batch(day %d)", e.Day, e.BatchDay)
			}
			remaining[e.BatchDay] += e.Qty
		case "consume":
			if e.Age > p.Shelf-1 {
				return fmt.Errorf("day %d: consumed batch(day %d) at age %d beyond shelf life %d", e.Day, e.BatchDay, e.Age, p.Shelf)
			}
			if err := olderEmpty(e.BatchDay); err != nil {
				return err
			}
			if remaining[e.BatchDay] < e.Qty {
				return fmt.Errorf("day %d: consumed %d of batch(day %d) with only %d on hand", e.Day, e.Qty, e.BatchDay, remaining[e.BatchDay])
			}
			remaining[e.BatchDay] -= e.Qty
		case "expire":
			if e.Age != p.Shelf {
				return fmt.Errorf("day %d: batch(day %d) expired at age %d, want %d", e.Day, e.BatchDay, e.Age, p.Shelf)
			}
			if remaining[e.BatchDay] < e.Qty {
				return fmt.Errorf("day %d: expired %d of batch(day %d) with only %d on hand", e.Day, e.Qty, e.BatchDay, remaining[e.BatchDay])
			}
			remaining[e.BatchDay] -= e.Qty
		}
	}
	return nil
}
