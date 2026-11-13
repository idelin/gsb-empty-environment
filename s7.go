package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

const (
	tpktHeaderLen = 4
	cotpHeaderLen = 3

	s7JobHeaderLen     = 10
	s7AckHeaderLen     = 12
	s7ProtocolID       = 0x32
	s7ROSCTRJob        = 0x01
	s7ROSCTRAck        = 0x02
	s7ROSCTRAckData    = 0x03
	s7ROSCTRUserData   = 0x07
	readVarFunction    = 0x04
	writeVarFunction   = 0x05
	downloadStart      = 0x1a
	downloadEnd        = 0x1c
	piServiceFunction  = 0x28
	stopCPUFunction    = 0x29
	minimumS7PacketLen = tpktHeaderLen + cotpHeaderLen + s7JobHeaderLen
)

var (
	// ErrS7Malformed means the packet cannot be safely interpreted as S7comm.
	ErrS7Malformed = errors.New("malformed S7 packet")
	// ErrS7Denied means the packet is well formed but the admission policy
	// does not allow it.
	ErrS7Denied = errors.New("S7 packet denied")
)

// S7Admission applies the S7comm job admission policy. Its zero value admits
// read-variable jobs only; use NewS7Admission to authorize an engineering
// station for write, download, and CPU-control jobs.
//
// An S7Admission is safe for concurrent reads after construction.
type S7Admission struct {
	engineeringStations map[netip.Addr]struct{}
}

// NewS7Admission creates an admission policy. Invalid engineering-station
// addresses are ignored, which fails closed for the dangerous function codes.
func NewS7Admission(engineeringStations ...string) *S7Admission {
	policy := &S7Admission{
		engineeringStations: make(map[netip.Addr]struct{}, len(engineeringStations)),
	}
	for _, station := range engineeringStations {
		addr, err := parseStationAddress(station)
		if err == nil {
			policy.engineeringStations[addr] = struct{}{}
		}
	}
	return policy
}

// Check validates the packet structure and admits or denies it according to
// the policy. A nil result means the packet may be forwarded. Callers should
// distinguish ErrS7Malformed from ErrS7Denied when logging.
func (p *S7Admission) Check(srcIP string, packet []byte) error {
	src, err := parseStationAddress(srcIP)
	if err != nil {
		return fmt.Errorf("%w: invalid source address %q", ErrS7Malformed, srcIP)
	}

	job, function, err := parseS7Packet(packet)
	if err != nil {
		return err
	}
	if !job {
		return nil
	}

	switch {
	case function == readVarFunction:
		return nil
	case isEngineeringOnlyFunction(function):
		if p != nil {
			if _, ok := p.engineeringStations[src]; ok {
				return nil
			}
		}
		return fmt.Errorf("%w: function 0x%02x from %s is engineering-station-only",
			ErrS7Denied, function, src)
	default:
		return fmt.Errorf("%w: function 0x%02x is not allowed", ErrS7Denied, function)
	}
}

// Allow is the boolean form of Check for forwarding loops that only need a
// permit/deny result.
func (p *S7Admission) Allow(srcIP string, packet []byte) bool {
	return p.Check(srcIP, packet) == nil
}

// CheckS7 is a convenience entry point for a forwarding loop with a fixed
// engineering-station allowlist.
func CheckS7(srcIP string, packet []byte, engineeringStations ...string) error {
	return NewS7Admission(engineeringStations...).Check(srcIP, packet)
}

// AdmitS7 reports whether the packet may be forwarded.
func AdmitS7(srcIP string, packet []byte, engineeringStations ...string) bool {
	return CheckS7(srcIP, packet, engineeringStations...) == nil
}

// ValidateS7 checks only protocol structure and length consistency. It does
// not apply the job function-code policy.
func ValidateS7(packet []byte) error {
	_, _, err := parseS7Packet(packet)
	return err
}

func parseS7Packet(packet []byte) (job bool, function byte, err error) {
	if len(packet) < minimumS7PacketLen {
		return false, 0, fmt.Errorf("%w: packet is %d bytes, need at least %d",
			ErrS7Malformed, len(packet), minimumS7PacketLen)
	}
	if packet[0] != 0x03 || packet[1] != 0x00 {
		return false, 0, fmt.Errorf("%w: invalid TPKT version/reserved bytes", ErrS7Malformed)
	}

	tpktLen := int(binary.BigEndian.Uint16(packet[2:4]))
	if tpktLen != len(packet) {
		return false, 0, fmt.Errorf("%w: TPKT length is %d, packet is %d bytes",
			ErrS7Malformed, tpktLen, len(packet))
	}

	// This policy handles the fixed three-byte COTP data header used for S7
	// communication: length indicator 2 and PDU type DT.
	if packet[4] != 2 || packet[5] != 0xf0 {
		return false, 0, fmt.Errorf("%w: unsupported COTP header", ErrS7Malformed)
	}

	s7 := packet[tpktHeaderLen+cotpHeaderLen:]
	if s7[0] != s7ProtocolID {
		return false, 0, fmt.Errorf("%w: invalid S7 protocol identifier 0x%02x",
			ErrS7Malformed, s7[0])
	}

	rosctr := s7[1]
	headerLen := s7JobHeaderLen
	switch rosctr {
	case s7ROSCTRAck, s7ROSCTRAckData:
		headerLen = s7AckHeaderLen
	}
	if len(s7) < headerLen {
		return false, 0, fmt.Errorf("%w: S7 PDU is shorter than its %d-byte header",
			ErrS7Malformed, headerLen)
	}

	paramLen := int(binary.BigEndian.Uint16(s7[6:8]))
	dataLen := int(binary.BigEndian.Uint16(s7[8:10]))
	if len(s7) != headerLen+paramLen+dataLen {
		return false, 0, fmt.Errorf(
			"%w: S7 PDU length is %d, header plus parameter/data length is %d",
			ErrS7Malformed, len(s7), headerLen+paramLen+dataLen)
	}

	switch rosctr {
	case s7ROSCTRJob:
		if paramLen == 0 {
			return false, 0, fmt.Errorf("%w: job has no parameter/function code", ErrS7Malformed)
		}
		return true, s7[headerLen], nil
	case s7ROSCTRAck, s7ROSCTRAckData, s7ROSCTRUserData:
		// Responses and userdata do not carry a job function code and are
		// allowed through so the forwarding loop can carry reply traffic.
		return false, 0, nil
	default:
		return false, 0, fmt.Errorf("%w: unsupported ROSCTR 0x%02x", ErrS7Denied, rosctr)
	}
}

func isEngineeringOnlyFunction(function byte) bool {
	return function == writeVarFunction ||
		(function >= downloadStart && function <= downloadEnd) ||
		function == piServiceFunction ||
		function == stopCPUFunction
}

func parseStationAddress(raw string) (netip.Addr, error) {
	host := strings.TrimSpace(raw)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")

	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, err
	}
	return addr.Unmap(), nil
}
