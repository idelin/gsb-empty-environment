package netsec

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

func TestContextCanonicalEncoding(t *testing.T) {
	context := Context{
		Tenant:    []byte{0x01, 0x00, 0x02},
		Purpose:   PurposeEncryption,
		Version:   0x0003,
		Direction: DirectionReceive,
		Epoch:     4,
	}

	encoded, err := context.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	const wantHex = "00000003" + "010002" +
		"00000001" + "01" +
		"00000002" + "0003" +
		"00000001" + "02" +
		"00000008" + "0000000000000004"
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatalf("decode expected encoding: %v", err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("unexpected encoding\ngot  %x\nwant %x", encoded, want)
	}

	viaMarshal, err := context.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	if !bytes.Equal(viaMarshal, encoded) {
		t.Fatal("MarshalBinary differs from Encode")
	}
}

func TestContextFieldBoundaryEncodingIsInjective(t *testing.T) {
	tenants := [][]byte{
		{0x00},
		{0x01},
		{0xff},
		{0x00, 0x00},
		{0x00, 0x01},
		{0x01, 0x00},
		{0xff, 0xff},
		{0x00, 0x00, 0x00},
		{0x00, 0x00, 0x01},
		{0x01, 0x00, 0x00},
		{0xff, 0x00, 0xff},
		{0x00, 0x00, 0x00, 0x01},
	}
	purposes := []Purpose{PurposeEncryption, PurposeAuthentication, PurposeTicket}
	versions := []uint16{0, 1, 0x0102, 0xffff}
	directions := []Direction{DirectionSend, DirectionReceive}
	epochs := []uint64{0, 1, 0x0102030405060708}

	seen := make(map[string]string)
	for _, tenant := range tenants {
		for _, purpose := range purposes {
			for _, version := range versions {
				for _, direction := range directions {
					for _, epoch := range epochs {
						context := Context{
							Tenant:    tenant,
							Purpose:   purpose,
							Version:   version,
							Direction: direction,
							Epoch:     epoch,
						}
						encoded, err := context.Encode()
						if err != nil {
							t.Fatalf("Encode(%v): %v", context, err)
						}
						key := string(encoded)
						label := fmt.Sprintf("tenant=%x purpose=%d version=%d direction=%d epoch=%d",
							tenant, purpose, version, direction, epoch)
						if previous, duplicate := seen[key]; duplicate {
							t.Fatalf("encoding collision between %s and %s: %x", previous, label, encoded)
						}
						seen[key] = label
					}
				}
			}
		}
	}
}

func TestContextValidation(t *testing.T) {
	valid := Context{
		Tenant:    []byte("tenant"),
		Purpose:   PurposeTicket,
		Version:   7,
		Direction: DirectionSend,
		Epoch:     9,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid context rejected: %v", err)
	}

	tests := []struct {
		name    string
		context Context
		want    error
	}{
		{
			name: "nil tenant",
			context: Context{
				Purpose:   PurposeEncryption,
				Direction: DirectionSend,
			},
			want: ErrEmptyTenant,
		},
		{
			name: "empty tenant",
			context: Context{
				Tenant:    []byte{},
				Purpose:   PurposeEncryption,
				Direction: DirectionSend,
			},
			want: ErrEmptyTenant,
		},
		{
			name: "tenant too long",
			context: Context{
				Tenant:    make([]byte, MaxTenantBytes+1),
				Purpose:   PurposeEncryption,
				Direction: DirectionSend,
			},
			want: ErrTenantTooLong,
		},
		{
			name: "unknown purpose zero",
			context: Context{
				Tenant:    []byte("tenant"),
				Purpose:   0,
				Direction: DirectionSend,
			},
			want: ErrUnknownPurpose,
		},
		{
			name: "unknown purpose four",
			context: Context{
				Tenant:    []byte("tenant"),
				Purpose:   4,
				Direction: DirectionSend,
			},
			want: ErrUnknownPurpose,
		},
		{
			name: "unknown purpose max",
			context: Context{
				Tenant:    []byte("tenant"),
				Purpose:   255,
				Direction: DirectionSend,
			},
			want: ErrUnknownPurpose,
		},
		{
			name: "unknown direction zero",
			context: Context{
				Tenant:    []byte("tenant"),
				Purpose:   PurposeEncryption,
				Direction: 0,
			},
			want: ErrUnknownDirection,
		},
		{
			name: "unknown direction three",
			context: Context{
				Tenant:    []byte("tenant"),
				Purpose:   PurposeEncryption,
				Direction: 3,
			},
			want: ErrUnknownDirection,
		},
		{
			name: "unknown direction max",
			context: Context{
				Tenant:    []byte("tenant"),
				Purpose:   PurposeEncryption,
				Direction: 255,
			},
			want: ErrUnknownDirection,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.context.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("Validate: got %v, want %v", err, test.want)
			}
			if _, err := test.context.Encode(); !errors.Is(err, test.want) {
				t.Fatalf("Encode: got %v, want %v", err, test.want)
			}
		})
	}

	maximum := Context{
		Tenant:    bytes.Repeat([]byte{0x5a}, MaxTenantBytes),
		Purpose:   PurposeAuthentication,
		Version:   0xffff,
		Direction: DirectionReceive,
		Epoch:     ^uint64(0),
	}
	encoded, err := maximum.Encode()
	if err != nil {
		t.Fatalf("maximum-length tenant rejected: %v", err)
	}
	if len(encoded) > MaxContextBytes {
		t.Fatalf("encoded context exceeded limit: %d", len(encoded))
	}
}

func TestContextEncodingCopiesTenant(t *testing.T) {
	tenant := []byte("tenant")
	context := Context{
		Tenant:    tenant,
		Purpose:   PurposeAuthentication,
		Version:   1,
		Direction: DirectionSend,
		Epoch:     1,
	}
	before, err := context.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for i := range tenant {
		tenant[i] = 0
	}
	after, err := context.Encode()
	if err != nil {
		t.Fatalf("Encode after mutation: %v", err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("encoding unexpectedly aliases the caller's tenant slice")
	}
}
