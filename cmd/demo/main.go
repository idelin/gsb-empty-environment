// Demo: vectorized filter + projection over a dictionary-encoded column.
//
//	status IN ('PAID', 'REFUNDED')
//
// The string predicate is pushed down through the dictionary into integer
// IDs once; the scan runs on the compact 1024-byte ID vector; strings are
// decoded back only when printing the final result.
package main

import (
	"fmt"

	"dictvec/dictenc"
)

func main() {
	dict := dictenc.NewDictionary("PENDING", "PAID", "REFUNDED")

	const rows = 1024
	values := make([]string, rows)
	statuses := []string{"PENDING", "PAID", "REFUNDED"}
	for i := range values {
		values[i] = statuses[i%3]
	}
	col := dictenc.NewColumn(dict, values)
	fmt.Printf("column: %d rows, dictionary-encoded to %d bytes (1 byte/row, %d distinct values)\n",
		col.Len(), len(col.IDs), dict.Len())

	// Predicate pushdown: 'PAID'->1, 'REFUNDED'->2, then a vectorized
	// integer-table scan over the 1024-byte ID array.
	mask := col.In("PAID", "REFUNDED")
	fmt.Printf("filter: status IN ('PAID','REFUNDED') -> integer mask, %d/%d rows matched\n",
		mask.Count(), col.Len())

	// Projection stays in integer-ID space.
	projected := col.Project(mask)
	fmt.Printf("project: %d rows kept, still dictionary IDs (no string touched yet)\n",
		projected.Len())

	// Only now, at output time, decode IDs back to strings.
	out := projected.Materialize()
	fmt.Printf("output: first 8 decoded values: %v\n", out[:8])
	fmt.Printf("output: last  4 decoded values: %v\n", out[len(out)-4:])
}
