package netsec

import (
	"encoding/binary"
	"errors"
	"net"
	"sync"
)

// PROFINET / DCP protocol constants.
const (
	EtherTypePROFINET uint16 = 0x8892
	tpid8021Q         uint16 = 0x8100

	// FrameIDSet is the DCP Set frame identifier guarded here. Per the task
	// contract, Set requests carried inside EtherType 0x8892 use FrameID
	// 0xFEFD. (The published PN-AL spec assigns 0xFEFC to Set and 0xFEFD to
	// Get; the constant is centralized so it can be adjusted in one place.)
	FrameIDSet uint16 = 0xFEFD

	FrameIDIdentifyRequest  uint16 = 0xFEFE
	FrameIDIdentifyResponse uint16 = 0xFEFF
)

const (
	svcGet      byte = 0x03
	svcSet      byte = 0x04
	svcIdentify byte = 0x05
	svcHello    byte = 0x06
)

const (
	stRequest  byte = 0x00
	stResponse byte = 0x01
)

// DCP block options / sub-options.
const (
	optIP               byte = 0x01
	optDevice           byte = 0x02
	optDHCP             byte = 0x03
	optControl          byte = 0x05
	optDeviceInitiative byte = 0x06
	optAllSelector      byte = 0xFF
	devNameOfStation    byte = 0x02
	devDeviceID         byte = 0x03
	devVendorID         byte = 0x04
)

// Decisions returned by ProcessFrame.
const (
	ActionAccept = "accept"
	ActionDrop   = "drop"
)

// Event kinds reported through Decision.Event.
const (
	EventAcceptedSet      = "accepted_set"
	EventBlockedSet       = "blocked_set"
	EventDuplicateAnswer  = "duplicate_answer"
	EventIdentityMismatch = "identity_mismatch"
	EventMalformedFrame   = "malformed_frame"
	EventUnknownStation   = "unknown_station"
)

var (
	errShortFrame    = errors.New("ethernet frame shorter than 14 bytes")
	errShortVLAN     = errors.New("vlan-tagged frame truncated")
	errDCPHeader     = errors.New("dcp header truncated")
	errDCPDataLength = errors.New("dcp data length exceeds frame")
	errBlockHeader   = errors.New("dcp block header truncated")
)

// DeviceIdentity is the engineering configuration of a PROFINET device.
type DeviceIdentity struct {
	VendorID uint16
	DeviceID uint16
}

// Config is the engineering database used to build a Guard.
type Config struct {
	// Stations maps the configured name of station to its expected identity.
	Stations map[string]DeviceIdentity
	// EngineeringStationMACs are the only MACs allowed to issue DCP Set
	// requests (e.g. the engineering workstation / PLC configuration tool).
	EngineeringStationMACs []string
}

// Event describes a security-relevant observation.
type Event struct {
	Kind          string
	Reason        string
	FrameID       uint16
	ServiceID     byte
	SrcMAC        string
	NameOfStation string
	VendorID      uint16
	DeviceID      uint16
	// FirstMAC / SecondMAC are populated for EventDuplicateAnswer.
	FirstMAC  string
	SecondMAC string
}

// Decision is the guard verdict for one frame.
type Decision struct {
	Action string
	Reason string
	Event  *Event
}

type dcpBlock struct {
	Option  byte
	Sub     byte
	Length  int
	Payload []byte
}

type dcpFrame struct {
	FrameID     uint16
	ServiceID   byte
	ServiceType byte
	XID         uint32
	Response    uint16
	DataLen     uint16
	Blocks      []dcpBlock
}

type stationState struct {
	macs     map[string]bool
	conflict bool
}

// Guard is a layer-2 DCP guard. ProcessFrame is safe for concurrent use by
// packet capture goroutines.
type Guard struct {
	mu       sync.RWMutex
	stations map[string]DeviceIdentity
	engMACs  map[string]bool
	answers  map[string]*stationState
}

// New builds a Guard from the engineering configuration. MAC addresses are
// canonicalized, so mixed dash/colon notation is accepted.
func New(cfg Config) (*Guard, error) {
	g := &Guard{
		stations: make(map[string]DeviceIdentity, len(cfg.Stations)),
		engMACs:  make(map[string]bool),
		answers:  make(map[string]*stationState),
	}
	for name, id := range cfg.Stations {
		g.stations[name] = id
	}
	for _, mac := range cfg.EngineeringStationMACs {
		if err := g.RegisterEngineeringStation(mac); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// RegisterEngineeringStation adds a MAC allowed to send DCP Set requests.
func (g *Guard) RegisterEngineeringStation(mac string) error {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.engMACs[hw.String()] = true
	g.mu.Unlock()
	return nil
}

// StationConflict reports whether two distinct MACs have answered with the
// same name of station. The controller must not connect while true.
func (g *Guard) StationConflict(nameOfStation string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	st := g.answers[nameOfStation]
	return st != nil && st.conflict
}

// StationMACs returns the MACs observed answering for a name of station.
func (g *Guard) StationMACs(nameOfStation string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	st := g.answers[nameOfStation]
	if st == nil {
		return nil
	}
	macs := make([]string, 0, len(st.macs))
	for mac := range st.macs {
		macs = append(macs, mac)
	}
	return macs
}

// ProcessFrame consumes one raw Ethernet frame (as delivered by an AF_PACKET
// / BPF layer-2 capture) and returns the guard verdict.
func (g *Guard) ProcessFrame(frame []byte) Decision {
	srcMAC, etherType, payload, err := parseEthernet(frame)
	if err != nil {
		return drop(Event{Kind: EventMalformedFrame, Reason: err.Error()})
	}
	if etherType != EtherTypePROFINET {
		return Decision{Action: ActionAccept, Reason: "not a profinet frame"}
	}

	dcp, err := parseDCP(payload)
	if err != nil {
		return drop(Event{
			Kind:    EventMalformedFrame,
			Reason:  err.Error(),
			FrameID: frameIDOrZero(payload),
			SrcMAC:  srcMAC.String(),
		})
	}

	switch dcp.ServiceID {
	case svcSet:
		return g.processSet(dcp, srcMAC)
	case svcIdentify:
		if dcp.ServiceType == stResponse {
			return g.processIdentifyResponse(dcp, srcMAC)
		}
	}
	return Decision{Action: ActionAccept, Reason: "dcp frame not guarded"}
}

func drop(ev Event) Decision {
	return Decision{Action: ActionDrop, Reason: ev.Reason, Event: &ev}
}

func frameIDOrZero(payload []byte) uint16 {
	if len(payload) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(payload[0:2])
}

func (g *Guard) processSet(dcp *dcpFrame, srcMAC net.HardwareAddr) Decision {
	base := Event{
		FrameID:   dcp.FrameID,
		ServiceID: svcSet,
		SrcMAC:    srcMAC.String(),
	}
	if dcp.ServiceType != stRequest || dcp.FrameID != FrameIDSet {
		return Decision{Action: ActionAccept, Reason: "not a guarded set request"}
	}

	g.mu.RLock()
	registered := g.engMACs[srcMAC.String()]
	g.mu.RUnlock()
	if !registered {
		base.Kind = EventBlockedSet
		base.Reason = "dcp set request from unregistered engineering station"
		if name, ok := stationNameFromBlocks(dcp.Blocks); ok {
			base.NameOfStation = name
		}
		return drop(base)
	}

	name, ok := stationNameFromBlocks(dcp.Blocks)
	if !ok {
		base.Kind = EventMalformedFrame
		base.Reason = "set request lacks a valid name of station block"
		return drop(base)
	}
	base.Kind = EventAcceptedSet
	base.Reason = "set request from registered engineering station"
	base.NameOfStation = name
	return Decision{Action: ActionAccept, Reason: base.Reason, Event: &base}
}

func (g *Guard) processIdentifyResponse(dcp *dcpFrame, srcMAC net.HardwareAddr) Decision {
	base := Event{
		FrameID:   dcp.FrameID,
		ServiceID: svcIdentify,
		SrcMAC:    srcMAC.String(),
	}

	name, hasName := stationNameFromBlocks(dcp.Blocks)
	vendorID, deviceID, hasIdentity := identityFromBlocks(dcp.Blocks)
	if !hasName || !hasIdentity {
		base.Kind = EventMalformedFrame
		base.Reason = "identify response missing name of station or identity blocks"
		return drop(base)
	}
	base.NameOfStation = name
	base.VendorID = vendorID
	base.DeviceID = deviceID

	g.mu.Lock()
	defer g.mu.Unlock()

	expected, known := g.stations[name]
	if !known {
		base.Kind = EventUnknownStation
		base.Reason = "identify response for station absent from engineering config"
		return drop(base)
	}
	if vendorID != expected.VendorID || deviceID != expected.DeviceID {
		base.Kind = EventIdentityMismatch
		base.Reason = "vendor/device id does not match engineering config"
		return drop(base)
	}

	st := g.answers[name]
	if st == nil {
		st = &stationState{macs: make(map[string]bool)}
		g.answers[name] = st
	}

	src := srcMAC.String()
	if !st.macs[src] && len(st.macs) > 0 {
		var other string
		for seen := range st.macs {
			other = seen
			break
		}
		st.macs[src] = true
		st.conflict = true
		base.Kind = EventDuplicateAnswer
		base.Reason = "two distinct macs answered with the same name of station"
		base.FirstMAC = other
		base.SecondMAC = src
		return drop(base)
	}
	st.macs[src] = true
	return Decision{Action: ActionAccept, Reason: "unique answer matching engineering config"}
}

func parseEthernet(frame []byte) (net.HardwareAddr, uint16, []byte, error) {
	if len(frame) < 14 {
		return nil, 0, nil, errShortFrame
	}
	srcMAC := net.HardwareAddr(append([]byte(nil), frame[6:12]...))
	etherType := binary.BigEndian.Uint16(frame[12:14])
	offset := 14
	if etherType == tpid8021Q {
		if len(frame) < 18 {
			return nil, 0, nil, errShortVLAN
		}
		etherType = binary.BigEndian.Uint16(frame[16:18])
		offset = 18
	}
	return srcMAC, etherType, frame[offset:], nil
}

func parseDCP(payload []byte) (*dcpFrame, error) {
	if len(payload) < 12 {
		return nil, errDCPHeader
	}
	frame := &dcpFrame{
		FrameID:     binary.BigEndian.Uint16(payload[0:2]),
		ServiceID:   payload[2],
		ServiceType: payload[3],
		XID:         binary.BigEndian.Uint32(payload[4:8]),
		Response:    binary.BigEndian.Uint16(payload[8:10]),
		DataLen:     binary.BigEndian.Uint16(payload[10:12]),
	}
	if int(frame.DataLen) > len(payload)-12 {
		return nil, errDCPDataLength
	}
	blocks, err := parseDCPBlocks(payload[12 : 12+int(frame.DataLen)])
	if err != nil {
		return nil, err
	}
	frame.Blocks = blocks
	return frame, nil
}

func parseDCPBlocks(data []byte) ([]dcpBlock, error) {
	blocks := make([]dcpBlock, 0)
	seen := make(map[[2]byte]bool)
	for offset := 0; offset < len(data); {
		if offset+4 > len(data) {
			return nil, errBlockHeader
		}
		option := data[offset]
		suboption := data[offset+1]
		length := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))

		start := offset + 4
		end := start + length
		if end > len(data) {
			return nil, errors.New("dcp block length exceeds frame bounds")
		}
		key := [2]byte{option, suboption}
		if seen[key] {
			return nil, errors.New("duplicate dcp option/suboption block")
		}
		seen[key] = true

		blocks = append(blocks, dcpBlock{
			Option:  option,
			Sub:     suboption,
			Length:  length,
			Payload: data[start:end],
		})
		offset = end
		if length%2 == 1 {
			offset++ // single alignment padding byte
		}
	}
	return blocks, nil
}

func stationNameFromBlocks(blocks []dcpBlock) (string, bool) {
	for _, block := range blocks {
		if block.Option == optDevice && block.Sub == devNameOfStation && block.Length > 0 {
			return string(block.Payload), true
		}
	}
	return "", false
}

func identityFromBlocks(blocks []dcpBlock) (vendorID, deviceID uint16, ok bool) {
	hasVendor, hasDevice := false, false
	for _, block := range blocks {
		if block.Option != optDevice || block.Length < 2 {
			continue
		}
		value := binary.BigEndian.Uint16(block.Payload[:2])
		switch block.Sub {
		case devVendorID:
			vendorID, hasVendor = value, true
		case devDeviceID:
			deviceID, hasDevice = value, true
		}
	}
	return vendorID, deviceID, hasVendor && hasDevice
}
