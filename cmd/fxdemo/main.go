// Command fxdemo converts a sample bill EUR->USD and prints the
// full audit report: exact fractional intermediates, per-node
// rounding gain/loss, and the per-item vs aggregate difference.
package main

import (
	"fmt"
	"os"

	"fxacct"
)

func main() {
	engine := fxacct.Engine{Steps: []fxacct.Step{
		fxacct.Convert{From: "EUR", To: "USD", Rate: fxacct.MustRat("10773/10000")},
		fxacct.Round{MinorUnit: 2, Mode: fxacct.RoundHalfAwayFromZero},
	}}

	charge := fxacct.LineItem{ID: "inv-1001", Amount: fxacct.MustRat("5"), Currency: "EUR"}
	items := []fxacct.LineItem{
		{ID: "fee-1", Amount: fxacct.MustRat("4/100"), Currency: "EUR"},
		{ID: "fee-2", Amount: fxacct.MustRat("4/100"), Currency: "EUR"},
		charge,
		charge.Reverse("inv-1001-void"), // correction of a posted line
	}

	cmp, err := engine.Compare(items)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(cmp.Report())
}
