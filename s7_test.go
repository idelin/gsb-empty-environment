package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

const (
	engineeringStation = "192.168.10.5"
	otherStation       = "192.168.10.6"
)

func TestS7Admission(t *testing.T) {
	policy := NewS7Admission(engineeringStation)

	tests := []struct {
		name    string
		src     string
		packet  []byte
		wantErr error
	}{
		{
			name:   "read variable from any station",
			src:    otherStation,
			packet: buildJob(0x04, make([]byte, 13), nil),
		},
		{
			name:    "write variable from non-engineering station",
			src:     otherStation,
			packet:  buildJob(0x05, make([]byte, 13), make([]byte, 4)),
			wantErr: ErrS7Denied,
		},
		{
			name:   "write variable from engineering station",
			src:    engineeringStation,
			packet: buildJob(0x05, make([]byte, 13), make([]byte, 4)),
		},
		{
			name:    "stop CPU from non-engineering station",
			src:     otherStation,
			packet:  buildJob(0x29, make([]byte, 7), nil),
			wantErr: ErrS7Denied,
		},
		{
			name:   "stop CPU from engineering station",
			src:    engineeringStation,
			packet: buildJob(0x29, make([]byte, 7), nil),
		},
		{
			name:    "PI service from non-engineering station",
			src:     otherStation,
			packet:  buildJob(0x28, make([]byte, 7), nil),
			wantErr: ErrS7Denied,
		},
		{
			name:    "unknown job function is denied by default",
			src:     engineeringStation,
			packet:  buildJob(0x06, make([]byte, 13), nil),
			wantErr: ErrS7Denied,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := policy.Check(tc.src, tc.packet)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Check() error = %v, want %v", err, tc.wantErr)
			}
			if got := policy.Allow(tc.src, tc.packet); got != (tc.wantErr == nil) {
				t.Fatalf("Allow() = %v, want %v", got, tc.wantErr == nil)
			}
		})
	}
}

func TestS7DownloadRequiresEngineeringStation(t *testing.T) {
	policy := NewS7Admission(engineeringStation)
	for function := byte(0x1a); function <= 0x1c; function++ {
		t.Run(fmt.Sprintf("function_0x%02x", function), func(t *testing.T) {
			packet := buildJob(function, make([]byte, 5), nil)
			if err := policy.Check(otherStation, packet); !errors.Is(err, ErrS7Denied) {
				t.Fatalf("Check() from other station error = %v, want %v", err, ErrS7Denied)
			}
			if err := policy.Check(engineeringStation, packet); err != nil {
				t.Fatalf("Check() from engineering station error = %v", err)
			}
		})
	}
}

func TestS7MalformedLengths(t *testing.T) {
	valid := buildJob(0x04, make([]byte, 13), nil)

	badTPKTLength := append([]byte(nil), valid...)
	binary.BigEndian.PutUint16(badTPKTLength[2:4], uint16(len(badTPKTLength)-1))

	badParamLength := append([]byte(nil), valid...)
	binary.BigEndian.PutUint16(badParamLength[13:15], 15)

	badDataLength := append([]byte(nil), valid...)
	binary.BigEndian.PutUint16(badDataLength[15:17], 1)

	extraTrailingByte := append(append([]byte(nil), valid...), 0)

	tests := []struct {
		name   string
		packet []byte
	}{
		{name: "short packet", packet: valid[:minimumS7PacketLen-1]},
		{name: "declared TPKT length mismatch", packet: badTPKTLength},
		{name: "S7 parameter length mismatch", packet: badParamLength},
		{name: "S7 data length mismatch", packet: badDataLength},
		{name: "trailing bytes after TPKT length", packet: extraTrailingByte},
	}

	policy := NewS7Admission(engineeringStation)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := policy.Check(engineeringStation, tc.packet); !errors.Is(err, ErrS7Malformed) {
				t.Fatalf("Check() error = %v, want %v", err, ErrS7Malformed)
			}
		})
	}
}

func TestS7ResponseTrafficPasses(t *testing.T) {
	packet := buildResponse(0x03, make([]byte, 4))
	if err := NewS7Admission().Check(otherStation, packet); err != nil {
		t.Fatalf("response Check() error = %v", err)
	}
}

func buildJob(function byte, parameterTail []byte, data []byte) []byte {
	parameter := append([]byte{function}, parameterTail...)
	return buildPDU(0x01, parameter, data)
}

func buildResponse(rosctr byte, data []byte) []byte {
	header := make([]byte, 12)
	header[0] = s7ProtocolID
	header[1] = rosctr
	binary.BigEndian.PutUint16(header[4:6], 1)
	binary.BigEndian.PutUint16(header[8:10], uint16(len(data)))
	return buildPacket(header, nil, data)
}

func buildPDU(rosctr byte, parameter, data []byte) []byte {
	header := make([]byte, s7JobHeaderLen)
	header[0] = s7ProtocolID
	header[1] = rosctr
	binary.BigEndian.PutUint16(header[4:6], 1)
	binary.BigEndian.PutUint16(header[6:8], uint16(len(parameter)))
	binary.BigEndian.PutUint16(header[8:10], uint16(len(data)))
	return buildPacket(header, parameter, data)
}

func buildPacket(header, parameter, data []byte) []byte {
	s7Len := len(header) + len(parameter) + len(data)
	packet := make([]byte, tpktHeaderLen+cotpHeaderLen+s7Len)
	packet[0] = 0x03
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	packet[4] = 0x02
	packet[5] = 0xf0
	packet[6] = 0x80
	copy(packet[7:], header)
	copy(packet[7+len(header):], parameter)
	copy(packet[7+len(header)+len(parameter):], data)
	return packet
}
