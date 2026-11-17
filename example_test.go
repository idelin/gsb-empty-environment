package netsec_test

import (
	"errors"
	"fmt"

	netsec "example.org/netsec/q-024"
)

// Example shows the trusted host assigning pages, a device domain mapping
// them, and a device DMA request being validated and executed entirely in
// software.
func Example() {
	const (
		tenant netsec.TenantID = 7
		device netsec.DeviceID = 3
	)

	// Trusted setup: ownership table and a per-device translation domain.
	host := netsec.NewHost()
	mu := netsec.NewIOMMU(host)
	domain, err := mu.NewDomain(device)
	if err != nil {
		panic(err)
	}

	// Physical frames 0x1000 and 0x2000 belong to the tenant.
	if err := host.Assign(0x1000, tenant); err != nil {
		panic(err)
	}
	if err := host.Assign(0x2000, tenant); err != nil {
		panic(err)
	}

	// Device IOVA frame 0 maps to physical frame 0x1000, read/write.
	if err := domain.Map(0, 0x1000, netsec.PermRead|netsec.PermWrite, tenant); err != nil {
		panic(err)
	}
	// IOVA frame 1 maps to physical frame 0x2000 so the transfer can cross.
	if err := domain.Map(0x1000, 0x2000, netsec.PermRead|netsec.PermWrite, tenant); err != nil {
		panic(err)
	}

	// A scatter-gather request crossing the page boundary.
	list := []netsec.Segment{{IOVA: 0x0FF0, Len: 32}}
	var touched []uint64
	cb := func(op netsec.MemOp) error {
		touched = append(touched, uint64(op.Phys))
		return nil
	}
	res, err := mu.Transfer(device, list, true, cb)
	if err != nil {
		panic(err)
	}
	fmt.Printf("gen=%d ops=%d bytes=%d phys=%x\n",
		res.Generation, res.Operations, res.Bytes, touched)

	// An attacker request into an unmapped IOVA fails before any callback.
	_, err = mu.Transfer(device, []netsec.Segment{{IOVA: 0x9000, Len: 1}}, false, cb)
	fmt.Println("unmapped:", errors.Is(err, netsec.ErrUnmapped))

	// Output:
	// gen=3 ops=2 bytes=32 phys=[1ff0 2000]
	// unmapped: true
}
