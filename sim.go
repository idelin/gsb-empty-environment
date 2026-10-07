package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Inventory model
//
// Shelf life `Shelf` (d days): a unit produced on day t is usable on days
// t, t+1, ..., t+d-1. Units still on hand at the end of their last usable
// day expire (counted as waste, no holding cost).
//
// Inventory at the START of a day (before production) is an age vector
// inv[0..Shelf-2], where inv[a-1] = units of age a (produced a days ago).
// Fresh production on the day itself has age 0.
//
// Consumption is FEFO (earliest-expiry-first = oldest age first).
// Stockouts are forbidden: a day is infeasible if on-hand + production
// cannot cover demand.
//
// Holding cost is charged per unit that survives the day (after consumption
// and after removing expired units).
// ---------------------------------------------------------------------------

// Batch is one lot of units sharing the same age (list-based representation,
// used by the independent brute-force simulator).
type Batch struct {
	Age int
	Qty int
}

func sumInts(v []int) int {
	s := 0
	for _, x := range v {
		s += x
	}
	return s
}

func sumBatches(bs []Batch) int {
	s := 0
	for _, b := range bs {
		s += b.Qty
	}
	return s
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// simulateDay advances one day on the age-vector representation.
//
// inv: start-of-day inventory, inv[a-1] = units of age a (a = 1..Shelf-1).
// x:   units produced today (age 0).
// demand: today's demand, consumed FEFO (oldest age first).
//
// Returns next-day start inventory (ages 1..Shelf-1), expired units,
// consumed-by-age (index = age 0..Shelf-1) and feasibility.
func simulateDay(inv []int, x, demand, shelf int) (next []int, expired int, consumed []int, ok bool) {
	if sumInts(inv)+x < demand {
		return nil, 0, nil, false
	}
	cur := make([]int, shelf) // cur[a] = on-hand units of age a right now
	cur[0] = x
	for a := 1; a < shelf; a++ {
		cur[a] = inv[a-1]
	}
	consumed = make([]int, shelf)
	rem := demand
	for a := shelf - 1; a >= 0 && rem > 0; a-- {
		take := minInt(cur[a], rem)
		cur[a] -= take
		consumed[a] += take
		rem -= take
	}
	expired = cur[shelf-1] // leftovers at their last usable day expire
	next = make([]int, shelf-1)
	for a := 1; a < shelf; a++ {
		next[a-1] = cur[a-1] // units of age a-1 today become age a tomorrow
	}
	return next, expired, consumed, true
}

// simulateDayBatches is an independent, list-of-lots implementation of the
// same day transition. It exists to cross-check simulateDay (batch ledger
// verification) and to drive the brute-force solvers.
func simulateDayBatches(batches []Batch, x, demand, shelf int) (next []Batch, expired int, ok bool) {
	total := x + sumBatches(batches)
	if total < demand {
		return nil, 0, false
	}
	lots := append([]Batch{}, batches...)
	if x > 0 {
		lots = append(lots, Batch{Age: 0, Qty: x})
	}
	// Oldest first (FEFO).
	sort.Slice(lots, func(i, j int) bool { return lots[i].Age > lots[j].Age })
	rem := demand
	next = []Batch{}
	expired = 0
	for _, b := range lots {
		take := minInt(b.Qty, rem)
		rem -= take
		left := b.Qty - take
		if left == 0 {
			continue
		}
		if b.Age+1 >= shelf {
			expired += left // reached end of shelf life
		} else {
			next = append(next, Batch{Age: b.Age + 1, Qty: left})
		}
	}
	return next, expired, true
}

// batchesToInv converts the list representation to the age-vector one.
func batchesToInv(batches []Batch, shelf int) []int {
	inv := make([]int, shelf-1)
	for _, b := range batches {
		if b.Age >= 1 && b.Age <= shelf-1 {
			inv[b.Age-1] += b.Qty
		}
	}
	return inv
}

func invKey(inv []int) string {
	parts := make([]string, len(inv))
	for i, v := range inv {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

// fmtInv renders an age vector as {a1:2 a2:5}.
func fmtInv(inv []int) string {
	if len(inv) == 0 {
		return "{}"
	}
	parts := make([]string, len(inv))
	for i, v := range inv {
		parts[i] = fmt.Sprintf("a%d:%d", i+1, v)
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// fmtConsumed renders consumed-by-age as {a0:1 a1:2 ...} (age 0 = fresh).
func fmtConsumed(consumed []int) string {
	parts := make([]string, len(consumed))
	for i, v := range consumed {
		parts[i] = fmt.Sprintf("a%d:%d", i, v)
	}
	return "{" + strings.Join(parts, " ") + "}"
}
