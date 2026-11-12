package netsec_test

import (
	"testing"

	"example.org/netsec/q0253"
)

func writePropertyAPDU(objectType uint16, instance uint32, property uint32, priority byte) []byte {
	objectID := []byte{
		byte(objectType >> 2),
		byte(objectType<<6) | byte(instance>>16),
		byte(instance >> 8),
		byte(instance),
	}
	apdu := []byte{
		0x00, 0x05, 0x0f, // Confirmed-Request, invoke 5, WriteProperty
		0x0c, // [0] object identifier, length 4
	}
	apdu = append(apdu, objectID...)
	apdu = append(apdu, 0x19, byte(property))         // [1] property identifier
	apdu = append(apdu, 0x3e)                         // [3] property value (constructed)
	apdu = append(apdu, 0x44, 0x00, 0x00, 0x00, 0x00) // Real 0.0
	apdu = append(apdu, 0x3f)                         // closing [3]
	if priority != 0 {
		apdu = append(apdu, 0x49, priority) // [4] priority
	}
	return apdu
}

func writePropertyMultipleAPDU(objectType uint16, instance uint32, writes []netsec.WriteAccess) []byte {
	objectID := []byte{
		byte(objectType >> 2),
		byte(objectType<<6) | byte(instance>>16),
		byte(instance >> 8),
		byte(instance),
	}
	apdu := []byte{0x00, 0x06, 0x10, 0x0c}
	apdu = append(apdu, objectID...)
	apdu = append(apdu, 0x1e) // [1] listOfProperties opening
	for _, write := range writes {
		apdu = append(apdu, 0x09, byte(write.Property)) // [0] property identifier
		apdu = append(apdu, 0x2e, 0x44, 0x00, 0x00, 0x00, 0x00, 0x3f)
		if write.Priority != 0 {
			apdu = append(apdu, 0x39, write.Priority) // [3] priority
		}
	}
	apdu = append(apdu, 0x1f) // closing listOfProperties
	return apdu
}

func apduPolicy(t *testing.T) *netsec.Policy {
	t.Helper()
	policy, err := netsec.NewPolicy(netsec.Config{
		AllowedTargets: []netsec.Target{{
			Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 1},
			Property: netsec.PropertyPresentValue,
		}},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func TestAPDUWritePropertyAdmission(t *testing.T) {
	policy := apduPolicy(t)
	apdu := writePropertyAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, uint32(netsec.PropertyPresentValue), 8)

	if decision := policy.AdmitAPDU(apdu); !decision.Allowed {
		t.Fatalf("registered APDU write denied: %+v", decision)
	}

	apdu = writePropertyAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, uint32(netsec.PropertyPresentValue), 1)
	decision := policy.AdmitAPDU(apdu)
	if decision.Allowed || decision.Reason != netsec.ReasonPriorityReserved {
		t.Fatalf("priority-1 APDU: expected reserved denial, got %+v", decision)
	}

	apdu = writePropertyAPDU(uint16(netsec.ObjectTypeAnalogValue), 2, uint32(netsec.PropertyPresentValue), 8)
	decision = policy.AdmitAPDU(apdu)
	if decision.Allowed || decision.Reason != netsec.ReasonUnregisteredTarget {
		t.Fatalf("unregistered APDU: expected unregistered denial, got %+v", decision)
	}
}

func TestAPDUWritePropertyMultipleSmuggling(t *testing.T) {
	policy := apduPolicy(t)
	presentValue := netsec.PropertyPresentValue

	good := writePropertyMultipleAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, []netsec.WriteAccess{
		{Property: presentValue, Priority: 8},
		{Property: presentValue, Priority: 12},
	})
	if decision := policy.AdmitAPDU(good); !decision.Allowed {
		t.Fatalf("valid WPM APDU denied: %+v", decision)
	}

	for _, tc := range []struct {
		name       string
		writes     []netsec.WriteAccess
		wantReason netsec.Reason
		wantMember int
	}{
		{
			name: "unregistered property appended",
			writes: []netsec.WriteAccess{
				{Property: presentValue, Priority: 8},
				{Property: netsec.PropertyPriorityArray, Priority: 8},
			},
			wantReason: netsec.ReasonUnregisteredTarget,
			wantMember: 1,
		},
		{
			name: "reserved priority appended",
			writes: []netsec.WriteAccess{
				{Property: presentValue, Priority: 8},
				{Property: presentValue, Priority: 2},
			},
			wantReason: netsec.ReasonPriorityReserved,
			wantMember: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apdu := writePropertyMultipleAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, tc.writes)
			decision := policy.AdmitAPDU(apdu)
			if decision.Allowed {
				t.Fatalf("smuggled WPM APDU was allowed")
			}
			if decision.Reason != tc.wantReason || decision.Member != tc.wantMember {
				t.Fatalf("got reason=%s member=%d, want reason=%s member=%d",
					decision.Reason, decision.Member, tc.wantReason, tc.wantMember)
			}
		})
	}
}

func TestAPDUControlServicesDenied(t *testing.T) {
	policy := apduPolicy(t)
	for _, service := range []netsec.Service{
		netsec.ServiceDeviceCommunicationControl,
		netsec.ServiceReinitializeDevice,
	} {
		apdu := []byte{0x00, 0x07, byte(service), 0x19, 0x00, 0x39, 0x00}
		decision := policy.AdmitAPDU(apdu)
		if decision.Allowed || decision.Reason != netsec.ReasonControlServiceDenied {
			t.Fatalf("%s: expected denial, got %+v", service, decision)
		}
	}
}

func TestBACnetIPFrameAdmission(t *testing.T) {
	policy := apduPolicy(t)
	apdu := writePropertyAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, uint32(netsec.PropertyPresentValue), 8)
	frameLength := 4 + 2 + len(apdu)
	frame := []byte{
		0x81, 0x0a, // BVLC original-unicast
		byte(frameLength >> 8), byte(frameLength), // BVLC length
		0x01, 0x00, // NPDU version 1, no destination/source
	}
	frame = append(frame, apdu...)

	if decision := policy.AdmitFrame(frame); !decision.Allowed {
		t.Fatalf("valid BACnet/IP frame denied: %+v", decision)
	}

	// Same frame with priority 1 must still be rejected after unwrapping.
	badAPDU := writePropertyAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, uint32(netsec.PropertyPresentValue), 1)
	badFrame := append([]byte(nil), frame[:6]...)
	badFrame = append(badFrame, badAPDU...)
	badFrame[2] = byte(len(badFrame) >> 8)
	badFrame[3] = byte(len(badFrame))
	if decision := policy.AdmitFrame(badFrame); decision.Allowed {
		t.Fatalf("priority-1 BACnet/IP frame was allowed")
	}
}

func TestMalformedAPDUFailsClosed(t *testing.T) {
	policy := apduPolicy(t)
	valid := writePropertyMultipleAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, []netsec.WriteAccess{
		{Property: netsec.PropertyPresentValue, Priority: 8},
	})
	for length := 0; length < len(valid); length++ {
		decision := policy.AdmitAPDU(valid[:length])
		if decision.Allowed {
			t.Fatalf("truncated APDU of length %d was allowed", length)
		}
	}

	invalid := [][]byte{
		nil,
		{0x81},
		{0x81, 0x0a, 0x00, 0x04},
		{0x00, 0x05, 0x0f},
		{0x00, 0x05, 0x0f, 0x0c, 0x00, 0x00},
		{0x00, 0x05, 0x0f, 0x0c, 0x00, 0x00, 0x00, 0x01, 0x19, 0x55, 0x3e, 0x44},
		{0x00, 0x05, 0x0f, 0x0c, 0x00, 0x00, 0x00, 0x01, 0x19, 0x55, 0x3e, 0x44, 0x00, 0x00, 0x00, 0x00, 0x3f, 0x49},
	}
	for i, apdu := range invalid {
		decision := policy.AdmitAPDU(apdu)
		if decision.Allowed || decision.Reason != netsec.ReasonMalformed {
			t.Fatalf("invalid APDU %d (%x): expected malformed denial, got %+v", i, apdu, decision)
		}
	}
}

func TestDecodeRequest(t *testing.T) {
	apdu := writePropertyAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, uint32(netsec.PropertyPresentValue), 8)
	req, err := netsec.DecodeRequest(apdu)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if req.Service != netsec.ServiceWriteProperty ||
		req.Object.Type != netsec.ObjectTypeAnalogValue ||
		req.Object.Instance != 1 ||
		req.Property != netsec.PropertyPresentValue ||
		req.Priority != 8 {
		t.Fatalf("decoded request mismatch: %+v", req)
	}
}

func FuzzAdmitAPDU(f *testing.F) {
	f.Add(writePropertyAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, uint32(netsec.PropertyPresentValue), 8))
	f.Add(writePropertyAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, uint32(netsec.PropertyPresentValue), 1))
	f.Add(writePropertyMultipleAPDU(uint16(netsec.ObjectTypeAnalogValue), 1, []netsec.WriteAccess{
		{Property: netsec.PropertyPresentValue, Priority: 8},
		{Property: netsec.PropertyPriorityArray, Priority: 8},
	}))
	f.Add([]byte{0x81, 0x0a, 0x00, 0x06, 0x01, 0x00})

	policy, err := netsec.NewPolicy(netsec.Config{
		AllowedTargets: []netsec.Target{{
			Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 1},
			Property: netsec.PropertyPresentValue,
		}},
	})
	if err != nil {
		f.Fatalf("NewPolicy: %v", err)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = policy.AdmitAPDU(data)
	})
}
