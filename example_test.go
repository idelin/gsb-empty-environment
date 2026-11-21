package indexvis_test

import (
	"fmt"

	indexvis "prescreen.local/ps-fff6434389"
)

func ExampleNewIndex() {
	snap := indexvis.Snapshot{
		UpperBound: 100,
		Known: map[indexvis.TxID]indexvis.Status{
			10: indexvis.Committed,
			11: indexvis.Aborted,
		},
	}
	deps := []indexvis.Edge{{From: 10, To: 12}}
	page := indexvis.NewPageSummary()
	_ = page.Add(10, indexvis.NoTransaction, 3)
	_ = page.Add(12, 11, 5)

	ix, err := indexvis.NewIndex(snap, deps, page)
	if err != nil {
		panic(err)
	}
	res, err := ix.Evaluate()
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Min, res.Max)
	// Output:
	// 8 8
}
