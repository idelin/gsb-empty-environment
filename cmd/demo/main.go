// Command demo runs the acceptance scenario against the bitemporal ledger.
package main

import (
	"fmt"
	"log"

	"bitemporal"
)

func main() {
	l := bitemporal.NewLedger()
	l.Add(bitemporal.Rule{ID: "p10", ValidFrom: 0, ValidTo: 20, PriceCents: 10, KnownAt: 2})
	l.Add(bitemporal.Rule{ID: "p15", ValidFrom: 5, ValidTo: 10, PriceCents: 15, KnownAt: 8})

	queries := []struct {
		validTime int64
		asOf      int64
	}{
		{7, 7},
		{7, 8},
		{10, 8},
	}
	for _, q := range queries {
		price, err := l.PriceAt(q.validTime, q.asOf)
		if err != nil {
			log.Fatalf("PriceAt(%d, %d): %v", q.validTime, q.asOf, err)
		}
		fmt.Printf("PriceAt(valid=%d, asOf=%d) = %d cents\n", q.validTime, q.asOf, price)
	}

	l.Retract("p15", 12)
	for _, asOf := range []int64{11, 12} {
		price, err := l.PriceAt(7, asOf)
		if err != nil {
			log.Fatalf("PriceAt(7, %d): %v", asOf, err)
		}
		fmt.Printf("after retracting p15 at tx 12: PriceAt(valid=7, asOf=%d) = %d cents\n", asOf, price)
	}
}
