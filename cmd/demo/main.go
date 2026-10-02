// Demo: vectorized IN-filter over a dictionary-encoded status column.
package main

import (
	"fmt"

	"dictcol"
)

func main() {
	statuses := []string{
		"PENDING", "PAID", "REFUNDED", "PAID",
		"PENDING", "REFUNDED", "PAID", "PENDING",
		"PAID", "PENDING",
	}

	col := dictcol.Encode(statuses)
	fmt.Printf("rows=%d encoded=%d bytes (1 byte/row)\n", col.Len(), len(col.Codes))
	fmt.Printf("dictionary: PENDING->%d PAID->%d REFUNDED->%d\n",
		col.Dict.Intern("PENDING"), col.Dict.Intern("PAID"), col.Dict.Intern("REFUNDED"))
	fmt.Printf("encoded codes: %v\n", col.Codes)

	// Predicate pushdown: 'status IN (PAID, REFUNDED)' -> integer ID mask scan.
	mask := col.FilterIn("PAID", "REFUNDED")
	fmt.Printf("\nfilter: status IN (PAID, REFUNDED)\n")
	fmt.Printf("bitmask: %08b (matched %d/%d rows)\n", mask[0], mask.Count(), col.Len())

	// Decode dictionary IDs back to strings only at final output.
	fmt.Printf("projected: %v\n", col.Project(mask))
}
