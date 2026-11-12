package netsec_test

import (
	"fmt"
	"math/big"

	netsec "example.org/netsec/q0327"
)

// ExampleCompiler shows an update host compiling region descriptors against a
// firmware image and recovering the original descriptor chain that writes into
// the protected boot region.
func ExampleCompiler() {
	const imageSize = uint64(1 << 16)

	descriptors := []netsec.Descriptor{
		{
			Name:   "firmware-payload",
			Base:   big.NewInt(0x8000),
			Offset: big.NewInt(0),
			Scale:  big.NewInt(1),
			Length: 0x4000,
		},
		{
			// start = 0xFFF8 + 2*8 = 0x10008: the run crosses the image
			// boundary and its tail lands back at physical byte 0 (boot).
			Name:   "calibration",
			Base:   big.NewInt(0xFFF8),
			Offset: big.NewInt(2),
			Scale:  big.NewInt(8),
			Length: 32,
		},
	}
	protected := []netsec.Region{
		{Name: "boot-block", Start: 0, End: 0x1000},
	}

	compiler, err := netsec.New(imageSize, netsec.WithModularDecode())
	if err != nil {
		panic(err)
	}
	report, err := compiler.Compile(descriptors, protected)
	if err != nil {
		panic(err)
	}

	for _, result := range report.Results {
		fmt.Printf("%s start=%s wrap(domain=%v rep=%v)\n",
			result.Name, result.Start, result.DomainWrap, result.RepresentationWrap)
	}
	for _, violation := range report.Violations {
		fmt.Printf("violation region=%s bytes=[%d,%d) via=%v\n",
			violation.Region, violation.Start, violation.End, violation.Descriptors)
	}

	// Output:
	// firmware-payload start=32768 wrap(domain=false rep=false)
	// calibration start=65544 wrap(domain=true rep=false)
	// violation region=boot-block bytes=[8,40) via=[calibration]
}
