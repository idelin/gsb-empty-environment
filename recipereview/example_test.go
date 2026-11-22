package recipereview_test

import (
	"fmt"

	"prescreen.local/ddd-2009-20261122-v2/recipereview"
)

// ExampleReview shows the release-gate flow over two small coexisting recipes.
func ExampleReview() {
	binary, _ := recipereview.NewAlphabet([]byte{0x00, 0x01})

	legacy := &recipereview.Recipe{
		ID:       1,
		Purpose:  "legacy-kdf",
		Version:  "v1",
		ReadOnly: true,
		Fields: map[string]*recipereview.Field{
			"label": {Name: "label", Alphabet: binary, MinLen: 0, MaxLen: 1},
		},
		Root: &recipereview.PrefixExpr{Field: "label"},
	}
	active := &recipereview.Recipe{
		ID:      2,
		Purpose: "rotating-kdf",
		Version: "v2",
		Fields: map[string]*recipereview.Field{
			"label": {Name: "label", Alphabet: binary, MinLen: 0, MaxLen: 1},
		},
		Root: &recipereview.SeqExpr{Parts: []recipereview.Expr{
			&recipereview.ConstExpr{Value: []byte{0x0f}},
			&recipereview.PrefixExpr{Field: "label"},
		}},
	}

	report, err := recipereview.Review([]*recipereview.Recipe{legacy, active},
		recipereview.NewCompatTable(), nil)
	if err != nil {
		fmt.Println("review error:", err)
		return
	}
	fmt.Println("safe:", report.Safe())

	info, _ := active.Encode(recipereview.Assignment{"label": {0x01}})
	decoded, _ := active.Decode(info)
	fmt.Printf("round trip: %x -> %x\n", info, decoded["label"])
	// Output:
	// safe: true
	// round trip: 0f0101 -> 01
}
