package netsec

import (
	"encoding/binary"
	"net"
	"testing"
)

var broadcastMAC = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

func mustMAC(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	mac, err := net.ParseMAC(s)
	if err != nil {
		t.Fatalf("parse mac %q: %v", s, err)
	}
	return mac
}

func be16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

// dcpBlock builds one DCP data block including the odd-length alignment byte.
func buildBlock(option, suboption byte, value []byte) []byte {
	b := []byte{option, suboption}
	b = binary.BigEndian.AppendUint16(b, uint16(len(value)))
	b = append(b, value...)
	if len(value)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func dcpPDU(frameID uint16, serviceID, serviceType byte, xid uint32, response uint16, data []byte) []byte {
	pdu := make([]byte, 12, 12+len(data))
	binary.BigEndian.PutUint16(pdu[0:2], frameID)
	pdu[2] = serviceID
	pdu[3] = serviceType
	binary.BigEndian.PutUint32(pdu[4:8], xid)
	binary.BigEndian.PutUint16(pdu[8:10], response)
	binary.BigEndian.PutUint16(pdu[10:12], uint16(len(data)))
	return append(pdu, data...)
}

func ethernet(dst, src net.HardwareAddr, etherType uint16, payload []byte) []byte {
	frame := make([]byte, 14+len(payload))
	copy(frame[0:6], dst)
	copy(frame[6:12], src)
	binary.BigEndian.PutUint16(frame[12:14], etherType)
	copy(frame[14:], payload)
	return frame
}

func ethernetVLAN(dst, src net.HardwareAddr, etherType uint16, payload []byte) []byte {
	frame := make([]byte, 18+len(payload))
	copy(frame[0:6], dst)
	copy(frame[6:12], src)
	binary.BigEndian.PutUint16(frame[12:14], tpid8021Q)
	binary.BigEndian.PutUint16(frame[14:16], 0) // priority/DEI/VID
	binary.BigEndian.PutUint16(frame[16:18], etherType)
	copy(frame[18:], payload)
	return frame
}

func nameBlock(name string) []byte {
	return buildBlock(optDevice, devNameOfStation, []byte(name))
}

func vendorBlock(vendorID uint16) []byte {
	return buildBlock(optDevice, devVendorID, be16(vendorID))
}

func deviceBlock(deviceID uint16) []byte {
	return buildBlock(optDevice, devDeviceID, be16(deviceID))
}

func setRequest(src net.HardwareAddr, blocks ...[]byte) []byte {
	data := make([]byte, 0)
	for _, b := range blocks {
		data = append(data, b...)
	}
	return ethernet(
		broadcastMAC,
		src,
		EtherTypePROFINET,
		dcpPDU(FrameIDSet, svcSet, stRequest, 1, 0, data),
	)
}

func identifyResponse(src net.HardwareAddr, blocks ...[]byte) []byte {
	data := make([]byte, 0)
	for _, b := range blocks {
		data = append(data, b...)
	}
	return ethernet(
		broadcastMAC,
		src,
		EtherTypePROFINET,
		dcpPDU(FrameIDIdentifyResponse, svcIdentify, stResponse, 2, 0, data),
	)
}

func testGuard(t *testing.T) *Guard {
	t.Helper()
	guard, err := New(Config{
		Stations: map[string]DeviceIdentity{
			"io-device-1": {VendorID: 0x002A, DeviceID: 0x1234},
			"pump-1x":     {VendorID: 0x002A, DeviceID: 0x1234},
		},
		EngineeringStationMACs: []string{"02:00:00:00:00:01"},
	})
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}
	return guard
}

func TestSetFromRegisteredEngineeringStationAccepted(t *testing.T) {
	guard := testGuard(t)
	eng := mustMAC(t, "02:00:00:00:00:01")
	frame := setRequest(eng, nameBlock("io-device-1"))

	decision := guard.ProcessFrame(frame)
	if decision.Action != ActionAccept {
		t.Fatalf("expected accept, got %s (%s)", decision.Action, decision.Reason)
	}
	if decision.Event == nil || decision.Event.Kind != EventAcceptedSet {
		t.Fatalf("expected accepted_set event, got %+v", decision.Event)
	}
	if decision.Event.NameOfStation != "io-device-1" {
		t.Fatalf("unexpected station name %q", decision.Event.NameOfStation)
	}
}

func TestSetFromUnknownMACBlocked(t *testing.T) {
	guard := testGuard(t)
	attacker := mustMAC(t, "02:00:00:00:00:99")
	frame := setRequest(attacker, nameBlock("io-device-1"))

	decision := guard.ProcessFrame(frame)
	if decision.Action != ActionDrop {
		t.Fatalf("expected drop, got %s", decision.Action)
	}
	if decision.Event == nil || decision.Event.Kind != EventBlockedSet {
		t.Fatalf("expected blocked_set event, got %+v", decision.Event)
	}
	if decision.Event.SrcMAC != "02:00:00:00:00:99" {
		t.Fatalf("unexpected src mac %q", decision.Event.SrcMAC)
	}
}

func TestRegisterEngineeringStationEnablesSet(t *testing.T) {
	guard := testGuard(t)
	extra := mustMAC(t, "02:00:00:00:00:02")

	if d := guard.ProcessFrame(setRequest(extra, nameBlock("io-device-1"))); d.Action != ActionDrop {
		t.Fatalf("expected drop before registration, got %s", d.Action)
	}
	if err := guard.RegisterEngineeringStation("02:00:00:00:00:02"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if d := guard.ProcessFrame(setRequest(extra, nameBlock("io-device-1"))); d.Action != ActionAccept {
		t.Fatalf("expected accept after registration, got %s", d.Action)
	}
}

func TestSetOnNonSetFrameIDNotGuarded(t *testing.T) {
	guard := testGuard(t)
	attacker := mustMAC(t, "02:00:00:00:00:99")
	data := nameBlock("io-device-1")
	frame := ethernet(
		broadcastMAC,
		attacker,
		EtherTypePROFINET,
		dcpPDU(0xFE00, svcSet, stRequest, 1, 0, data),
	)
	if d := guard.ProcessFrame(frame); d.Action != ActionAccept {
		t.Fatalf("only FrameID %#x must be guarded, got %s", FrameIDSet, d.Action)
	}
}

func TestUniqueIdentifyAnswerAccepted(t *testing.T) {
	guard := testGuard(t)
	device := mustMAC(t, "02:00:00:00:00:0a")
	frame := identifyResponse(
		device,
		nameBlock("io-device-1"),
		vendorBlock(0x002A),
		deviceBlock(0x1234),
	)

	decision := guard.ProcessFrame(frame)
	if decision.Action != ActionAccept {
		t.Fatalf("expected accept, got %s (%s)", decision.Action, decision.Reason)
	}
	if guard.StationConflict("io-device-1") {
		t.Fatal("no conflict expected for a single answer")
	}
	if macs := guard.StationMACs("io-device-1"); len(macs) != 1 || macs[0] != device.String() {
		t.Fatalf("unexpected answering macs %v", macs)
	}
}

func TestDuplicateMACAnsweringSameNameReportedAsConflict(t *testing.T) {
	guard := testGuard(t)
	legit := mustMAC(t, "02:00:00:00:00:0a")
	squatter := mustMAC(t, "02:00:00:00:00:0b")

	first := identifyResponse(legit, nameBlock("io-device-1"), vendorBlock(0x002A), deviceBlock(0x1234))
	if d := guard.ProcessFrame(first); d.Action != ActionAccept {
		t.Fatalf("first answer should be accepted, got %s (%s)", d.Action, d.Reason)
	}

	second := identifyResponse(squatter, nameBlock("io-device-1"), vendorBlock(0x002A), deviceBlock(0x1234))
	d := guard.ProcessFrame(second)
	if d.Action != ActionDrop {
		t.Fatalf("expected conflicting answer dropped, got %s", d.Action)
	}
	if d.Event == nil || d.Event.Kind != EventDuplicateAnswer {
		t.Fatalf("expected duplicate_answer event, got %+v", d.Event)
	}
	if d.Event.FirstMAC != legit.String() || d.Event.SecondMAC != squatter.String() {
		t.Fatalf("unexpected conflict pair %+v", d.Event)
	}
	if !guard.StationConflict("io-device-1") {
		t.Fatal("controller must observe a persistent station conflict")
	}
	if macs := guard.StationMACs("io-device-1"); len(macs) != 2 {
		t.Fatalf("expected both answering macs recorded, got %v", macs)
	}
}

func TestRepeatedAnswerFromSameMACNotAConflict(t *testing.T) {
	guard := testGuard(t)
	device := mustMAC(t, "02:00:00:00:00:0a")
	frame := identifyResponse(device, nameBlock("io-device-1"), vendorBlock(0x002A), deviceBlock(0x1234))

	for i := 0; i < 3; i++ {
		if d := guard.ProcessFrame(frame); d.Action != ActionAccept {
			t.Fatalf("repeat %d dropped: %s", i, d.Reason)
		}
	}
	if guard.StationConflict("io-device-1") {
		t.Fatal("repeated answers from one mac are not a conflict")
	}
}

func TestIdentifyVendorIDMismatchDropped(t *testing.T) {
	guard := testGuard(t)
	impostor := mustMAC(t, "02:00:00:00:00:0b")
	frame := identifyResponse(
		impostor,
		nameBlock("io-device-1"),
		vendorBlock(0xBEEF),
		deviceBlock(0x1234),
	)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventIdentityMismatch {
		t.Fatalf("expected identity_mismatch drop, got %+v", d)
	}
	if guard.StationConflict("io-device-1") {
		t.Fatal("mismatching answer must not enter the answer table")
	}
}

func TestIdentifyDeviceIDMismatchDropped(t *testing.T) {
	guard := testGuard(t)
	impostor := mustMAC(t, "02:00:00:00:00:0b")
	frame := identifyResponse(
		impostor,
		nameBlock("io-device-1"),
		vendorBlock(0x002A),
		deviceBlock(0xFFFF),
	)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventIdentityMismatch {
		t.Fatalf("expected identity_mismatch drop, got %+v", d)
	}
}

func TestIdentifyUnknownStationDropped(t *testing.T) {
	guard := testGuard(t)
	device := mustMAC(t, "02:00:00:00:00:0a")
	frame := identifyResponse(
		device,
		nameBlock("unconfigured-station"),
		vendorBlock(0x002A),
		deviceBlock(0x1234),
	)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventUnknownStation {
		t.Fatalf("expected unknown_station drop, got %+v", d)
	}
}

func TestBlockLengthOverflowDropped(t *testing.T) {
	guard := testGuard(t)
	attacker := mustMAC(t, "02:00:00:00:00:99")

	// Name block claims 60 bytes but the frame contains none of them.
	malformed := []byte{optDevice, devNameOfStation, 0x00, 0x3C}
	pdu := dcpPDU(FrameIDIdentifyResponse, svcIdentify, stResponse, 3, 0, malformed)
	frame := ethernet(broadcastMAC, attacker, EtherTypePROFINET, pdu)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventMalformedFrame {
		t.Fatalf("expected malformed drop for overflow block, got %+v", d)
	}
}

func TestDCPDataLengthOverflowDropped(t *testing.T) {
	guard := testGuard(t)
	attacker := mustMAC(t, "02:00:00:00:00:99")

	pdu := dcpPDU(FrameIDSet, svcSet, stRequest, 4, 0, nameBlock("io-device-1"))
	binary.BigEndian.PutUint16(pdu[10:12], 0xFFFF) // lies about data length
	frame := ethernet(broadcastMAC, attacker, EtherTypePROFINET, pdu)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventMalformedFrame {
		t.Fatalf("expected malformed drop for data length overflow, got %+v", d)
	}
}

func TestTruncatedBlockHeaderDropped(t *testing.T) {
	guard := testGuard(t)
	attacker := mustMAC(t, "02:00:00:00:00:99")

	pdu := dcpPDU(FrameIDIdentifyResponse, svcIdentify, stResponse, 5, 0, []byte{optDevice, devNameOfStation})
	frame := ethernet(broadcastMAC, attacker, EtherTypePROFINET, pdu)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventMalformedFrame {
		t.Fatalf("expected malformed drop for truncated block, got %+v", d)
	}
}

func TestDuplicateOptionBlockDropped(t *testing.T) {
	guard := testGuard(t)
	attacker := mustMAC(t, "02:00:00:00:00:99")

	data := append(nameBlock("io-device-1"), nameBlock("io-device-1")...)
	pdu := dcpPDU(FrameIDSet, svcSet, stRequest, 6, 0, data)
	frame := ethernet(broadcastMAC, attacker, EtherTypePROFINET, pdu)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventMalformedFrame {
		t.Fatalf("expected malformed drop for duplicate option, got %+v", d)
	}
}

func TestSetWithoutNameBlockDropped(t *testing.T) {
	guard := testGuard(t)
	eng := mustMAC(t, "02:00:00:00:00:01")

	pdu := dcpPDU(FrameIDSet, svcSet, stRequest, 7, 0, vendorBlock(0x002A))
	frame := ethernet(broadcastMAC, eng, EtherTypePROFINET, pdu)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventMalformedFrame {
		t.Fatalf("expected malformed drop for set without name block, got %+v", d)
	}
}

func TestOddLengthBlockAlignmentAccepted(t *testing.T) {
	guard := testGuard(t)
	device := mustMAC(t, "02:00:00:00:00:0a")

	// 4-letter name is even; force an odd one to exercise padding handling.
	frame := identifyResponse(
		device,
		nameBlock("pump-1x"),
		vendorBlock(0x002A),
		deviceBlock(0x1234),
	)
	d := guard.ProcessFrame(frame)
	if d.Action != ActionAccept {
		t.Fatalf("odd-length padded block should parse, got %s (%s)", d.Action, d.Reason)
	}
}

func TestVLANTaggedFrameParsed(t *testing.T) {
	guard := testGuard(t)
	eng := mustMAC(t, "02:00:00:00:00:01")
	pdu := dcpPDU(FrameIDSet, svcSet, stRequest, 8, 0, nameBlock("io-device-1"))
	frame := ethernetVLAN(broadcastMAC, eng, EtherTypePROFINET, pdu)

	d := guard.ProcessFrame(frame)
	if d.Action != ActionAccept {
		t.Fatalf("vlan-tagged set from engineering station should pass, got %s", d.Reason)
	}
}

func TestNonPROFINETFrameIgnored(t *testing.T) {
	guard := testGuard(t)
	src := mustMAC(t, "02:00:00:00:00:99")
	frame := ethernet(broadcastMAC, src, 0x0800, []byte("ipv4 payload"))

	if d := guard.ProcessFrame(frame); d.Action != ActionAccept {
		t.Fatalf("non-profinet traffic must be ignored, got %s", d.Action)
	}
}

func TestShortFrameDropped(t *testing.T) {
	guard := testGuard(t)
	d := guard.ProcessFrame([]byte{0x88, 0x92})
	if d.Action != ActionDrop || d.Event == nil || d.Event.Kind != EventMalformedFrame {
		t.Fatalf("expected malformed drop for short frame, got %+v", d)
	}
}
