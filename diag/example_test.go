package diag_test

import (
	"fmt"

	"example.org/netsec/q0488/diag"
)

func ExampleRenderAll() {
	input := []byte("user=ada token=hunter2 zone=cn extra=keepme")
	errs := []*diag.Error{
		diag.NewError("token", "credential rejected",
			diag.Interval{Start: 15, End: 16},
			[]diag.Secret{{Interval: diag.Interval{15, 22}, Label: "bearer"}},
			[]diag.Interval{{0, len(input)}}),
		diag.NewError("zone", "unknown zone",
			diag.Interval{Start: 25, End: 27}, nil,
			[]diag.Interval{{0, len(input)}}),
	}
	fmt.Print(diag.RenderAll(input, errs))
	// Output:
	// token: credential rejected
	// user=ada token=[REDACTED] zone=cn extra=k
	// zone: unknown zone
	// er=ada token=[REDACTED] zone=cn extra=keep
}
