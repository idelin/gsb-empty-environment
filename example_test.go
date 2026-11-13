package netsec_test

import (
	"fmt"

	"example.org/netsec/q0144"
)

func ExampleRemapper() {
	r := netsec.NewRemapper()
	if err := r.RegisterDomain(1, netsec.DomainConfig{
		Vectors: netsec.VectorRange{Min: 0x20, Max: 0x2f},
		VCPUs:   []netsec.VCPU{{ID: 0, Generation: 1}},
	}); err != nil {
		panic(err)
	}
	device, err := r.AttachDevice(netsec.MustRequesterID(0, 1, 0), 1)
	if err != nil {
		panic(err)
	}
	if err := device.Install(1, netsec.InstallRequest{
		TableIndex:     0,
		VCPU:           0,
		VCPUGeneration: 1,
		Vector:         0x21,
		DeliveryMode:   netsec.DeliveryFixed,
	}); err != nil {
		panic(err)
	}
	if err := device.SendInterrupt(0, 0x21, func(d netsec.Delivery) error {
		fmt.Println(d.DomainID, d.VCPUID, d.Vector)
		return nil
	}); err != nil {
		panic(err)
	}
	// Output: 1 0 33
}
