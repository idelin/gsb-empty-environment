package cfverify_test

import (
	"fmt"

	"example.org/domainreissue/q007/cfverify"
	"example.org/domainreissue/q007/cfverify/synthisa"
)

// ExampleVerify shows a review host verifying an image whose jump lands in
// the middle of an already decoded instruction.
func ExampleVerify() {
	image := synthisa.Image(
		synthisa.Calli(8),             // pc 1..3
		synthisa.Jmp(-1),              // pc 4..5 jumps to pc 3, mid-instruction
		synthisa.Ins(synthisa.OpHalt), // pc 6
		synthisa.Ins(synthisa.OpNop),  // pc 7
		synthisa.Ins(synthisa.OpHalt), // pc 8
	)

	result := cfverify.Verify(cfverify.Config{
		Image:         image,
		Entries:       []uint64{1},
		VersionOffset: 0,
		VersionByte:   synthisa.VersionByte,
		Decode:        synthisa.Decode,
	})

	fmt.Println(result.Verdict)
	fmt.Println(result.Conflict.Kind, result.Conflict.Target)
	fmt.Println(result.Witness)
	// Output:
	// conflict
	// mid-entry 3
	// [1 4 3]
}
