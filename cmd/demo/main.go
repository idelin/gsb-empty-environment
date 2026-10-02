// Command demo runs the acceptance scenario of the temporal KPI ledger:
// a Q1 strategy is recorded, retroactively re-weighted on day 45, and the
// score impact is printed together with the responsible version signature.
package main

import (
	"fmt"
	"log"
	"time"

	"kpiledger/ledger"
)

func main() {
	l, err := ledger.New(90) // Q1 evaluation period: days [1, 91)
	if err != nil {
		log.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	v1, err := l.Append("carol", "Q1 initial strategy", 1, base, []ledger.Assignment{
		{KPI: "A", Target: 100, Weight: 0.6, Valid: ledger.Interval{From: 1, To: 60}},
		{KPI: "A", Target: 100, Weight: 0.4, Valid: ledger.Interval{From: 60, To: 91}},
		{KPI: "B", Target: 50, Weight: 0.4, Valid: ledger.Interval{From: 1, To: 60}},
		{KPI: "B", Target: 50, Weight: 0.6, Valid: ledger.Interval{From: 60, To: 91}},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Day 45: strategy is retroactively adjusted for the whole quarter.
	v2, err := l.Append("alice", "retroactive reweight: A 0.4 / B 0.6 for whole quarter", 45,
		base.Add(44*24*time.Hour), []ledger.Assignment{
			{KPI: "A", Target: 100, Weight: 0.4, Valid: ledger.Interval{From: 1, To: 91}},
			{KPI: "B", Target: 50, Weight: 0.6, Valid: ledger.Interval{From: 1, To: 91}},
		})
	if err != nil {
		log.Fatal(err)
	}

	actuals := ledger.Actuals{"A": 110, "B": 40}

	fmt.Println("=== Versions (append-only, signed) ===")
	for _, v := range l.Versions() {
		fmt.Printf("v%d  author=%-6s recordedDay=%-3d sig=%.12s...  %s\n",
			v.ID, v.Author, v.RecordedDay, v.Signature(), v.Note)
	}

	fmt.Println("\n=== Point-in-time weights on day 10 ===")
	for _, id := range []int{v1.ID, v2.ID} {
		active, err := l.ActiveAt(id, 10)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("v%d: ", id)
		for _, a := range active {
			fmt.Printf("%s w=%.1f  ", a.KPI, a.Weight)
		}
		fmt.Println()
	}

	fmt.Println("\n=== Quarter-end weighted scores (historical replay) ===")
	for _, id := range []int{v1.ID, v2.ID} {
		sb, err := l.Score(id, actuals)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("v%d: effWeight A=%.4f B=%.4f  ratio A=%.2f B=%.2f  total=%.6f\n",
			id, sb.Weights["A"], sb.Weights["B"], sb.Ratios["A"], sb.Ratios["B"], sb.Total)
	}

	imp, err := l.Impact(v1.ID, v2.ID, actuals)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n=== Version impact (v1 -> v2) ===")
	fmt.Printf("score before: %.6f\n", imp.ScoreBefore)
	fmt.Printf("score after:  %.6f\n", imp.ScoreAfter)
	fmt.Printf("delta:        %+.6f\n", imp.Delta)
	fmt.Printf("responsible:  %s (recorded day %d)\n", imp.Author, imp.RecordedDay)
	fmt.Printf("signature:    %s\n", imp.Signature)
}
