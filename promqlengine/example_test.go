package promqlengine_test

import (
	"fmt"

	"prescreen.local/ps-d317ff30d1/promqlengine"
)

// Example shows the full workflow: build an in-memory series with a counter
// reset and a stale marker, parse a text query, and evaluate it.
func Example() {
	const end int64 = 60_000
	store := promqlengine.NewMemStorage()
	metric := promqlengine.NewLabels("__name__", "requests")
	store.AddSeries(metric,
		promqlengine.Point{T: 0, V: 0},
		promqlengine.Point{T: 10_000, V: 10},
		promqlengine.Point{T: 20_000, V: 5}, // counter reset
		promqlengine.Point{T: 30_000, V: 15},
		promqlengine.Point{T: 40_000, V: 25},
		promqlengine.Point{T: 50_000, V: promqlengine.StaleNaN()},
	)

	eng := promqlengine.NewEngine(store)

	// rate over the window ending before the stale marker.
	rateExpr := promqlengine.MustParseExpr(`rate(requests[40s])`)
	val, err := eng.Eval(rateExpr, 40_000)
	if err != nil {
		panic(err)
	}
	fmt.Printf("rate samples: %d\n", len(val.([]promqlengine.Sample)))

	// At and after the stale marker the instant selector emits nothing.
	inst := promqlengine.MustParseExpr(`requests`)
	v, err := eng.Eval(inst, end)
	if err != nil {
		panic(err)
	}
	fmt.Printf("instant samples after stale: %d\n", len(v.([]promqlengine.Sample)))

	// A step-aligned subquery.
	sq := promqlengine.MustParseExpr(`requests[30s:10s]`)
	mat, err := eng.Eval(sq, 40_000)
	if err != nil {
		panic(err)
	}
	for _, t := range mat.(promqlengine.Matrix).Series[0].Points {
		fmt.Printf("grid point: %ds\n", t.T/1000)
	}

	// Output:
	// rate samples: 1
	// instant samples after stale: 0
	// grid point: 20s
	// grid point: 30s
	// grid point: 40s
}
