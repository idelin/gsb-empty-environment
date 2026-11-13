package netsec

import (
	"errors"
	"testing"
)

func TestDomainCapabilityRejectsForeignAndForgedHandles(t *testing.T) {
	r := NewRemapper()
	config := DomainConfig{
		Vectors: VectorRange{Min: 0x20, Max: 0x2f},
		VCPUs:   []VCPU{{ID: 0, Generation: 1}},
	}
	domainA, err := r.NewDomain(domA, config)
	if err != nil {
		t.Fatalf("new domain A: %v", err)
	}
	domainB, err := r.NewDomain(domB, config)
	if err != nil {
		t.Fatalf("new domain B: %v", err)
	}
	a, err := r.AttachDeviceTo(reqA, domainA)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	req := InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	}
	if err := a.InstallAs(domainB, req); !errors.Is(err, ErrForeignDomain) {
		t.Fatalf("foreign capability install: got %v", err)
	}
	if err := a.InstallAs(&Domain{}, req); !errors.Is(err, ErrForeignDomain) {
		t.Fatalf("forged zero capability install: got %v", err)
	}
	if err := a.InstallBatchAs(domainB, []InstallRequest{req}); !errors.Is(err, ErrForeignDomain) {
		t.Fatalf("foreign capability batch: got %v", err)
	}
	if err := a.InstallAs(domainA, req); err != nil {
		t.Fatalf("owner capability install: %v", err)
	}

	replacement, err := r.ReassignTo(a, domainB)
	if err != nil {
		t.Fatalf("reassign to capability: %v", err)
	}
	if err := a.InstallAs(domainA, req); !errors.Is(err, ErrForeignDomain) {
		t.Fatalf("old capability after reassign: got %v", err)
	}
	if err := replacement.InstallAs(domainB, req); err != nil {
		t.Fatalf("new owner capability after reassign: %v", err)
	}
}
