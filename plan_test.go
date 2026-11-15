package netsec

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestPlanValidation(t *testing.T) {
	tests := []struct {
		name string
		plan Plan
		ok   bool
	}{
		{name: "no slots", plan: Plan{Slots: 0, SlotBytes: 64}},
		{name: "slot too small for tag", plan: Plan{Slots: 2, SlotBytes: MinSlotBytes - 1}},
		{name: "minimum slot", plan: Plan{Slots: 1, SlotBytes: MinSlotBytes}, ok: true},
		{name: "normal", plan: Plan{Slots: 8, SlotBytes: 64}, ok: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.plan.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
		})
	}
}

func TestPlanBoundsAndSchedule(t *testing.T) {
	plan := Plan{Slots: 4, SlotBytes: 64}
	if got, want := plan.ResultLimit(), uint64(3*64); got != want {
		t.Fatalf("ResultLimit() = %d, want %d", got, want)
	}
	if got, want := plan.DataBytesPerSlot(), 48; got != want {
		t.Fatalf("DataBytesPerSlot() = %d, want %d", got, want)
	}
	if got, want := plan.EncodableBytes(), uint64(3*48); got != want {
		t.Fatalf("EncodableBytes() = %d, want %d", got, want)
	}

	start := time.Unix(1_700_000_000, 0).UTC()
	schedule := Schedule{Start: start, Interval: 2 * time.Second}
	if got, want := schedule.SlotTime(3), start.Add(6*time.Second); !got.Equal(want) {
		t.Fatalf("SlotTime(3) = %v, want %v", got, want)
	}
}

func TestNonceIsRoundThenSlot(t *testing.T) {
	nonce := NonceFor(0x01020304, 0x0102030405060708)
	if len(nonce) != NonceSize {
		t.Fatalf("nonce size = %d, want %d", len(nonce), NonceSize)
	}
	want := []byte{
		0x01, 0x02, 0x03, 0x04,
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	}
	if !bytes.Equal(nonce, want) {
		t.Fatalf("nonce = %x, want %x", nonce, want)
	}
}

func TestAssociatedDataBindsRoundPlanAndSlot(t *testing.T) {
	plan := Plan{Slots: 5, SlotBytes: 64}
	base := AssociatedData(plan, 9, 3)

	for _, other := range []struct {
		name string
		aad  []byte
	}{
		{name: "round", aad: AssociatedData(plan, 10, 3)},
		{name: "slots", aad: AssociatedData(Plan{Slots: 6, SlotBytes: 64}, 9, 3)},
		{name: "slot bytes", aad: AssociatedData(Plan{Slots: 5, SlotBytes: 65}, 9, 3)},
		{name: "slot", aad: AssociatedData(plan, 9, 4)},
	} {
		t.Run(other.name, func(t *testing.T) {
			if bytes.Equal(base, other.aad) {
				t.Fatal("AAD did not change")
			}
		})
	}

	if binary.BigEndian.Uint32(base[len(aadDomain):]) != 9 {
		t.Fatal("round is not encoded immediately after the domain")
	}
}
