package netsec

import (
	"errors"
	"testing"
)

// FuzzForgedVectorNeverCrossesDomain drives the tenant-controlled MSI-X
// fields (table index and message vector) and asserts that a vector that does
// not match the requester-qualified entry is rejected before any callback.
func FuzzForgedVectorNeverCrossesDomain(f *testing.F) {
	f.Add(uint16(0), uint16(0))
	f.Add(uint16(1), uint16(0))
	f.Add(uint16(0), uint16(1))

	f.Fuzz(func(t *testing.T, registeredSelector, messageSelector uint16) {
		fx := newFixture(t)
		registered := Vector(0x20 + registeredSelector%0x10)
		message := Vector(0x20 + messageSelector%0x10)
		if err := fx.a.Install(domA, InstallRequest{
			TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
			Vector: registered, DeliveryMode: DeliveryFixed,
		}); err != nil {
			t.Fatalf("install: %v", err)
		}

		called := false
		err := fx.a.SendInterrupt(0, message, func(d Delivery) error {
			called = true
			if d.DomainID != domA {
				t.Fatalf("cross-domain delivery: %+v", d)
			}
			return nil
		})
		if message != registered {
			if !errors.Is(err, ErrVectorMismatch) {
				t.Fatalf("forged vector: got %v, want ErrVectorMismatch", err)
			}
			if called {
				t.Fatal("forged vector reached the callback")
			}
			return
		}
		if err != nil {
			t.Fatalf("matching vector: %v", err)
		}
	})
}
