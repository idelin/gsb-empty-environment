package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestParseBindingHappyPath(t *testing.T) {
	main := append(tlv(OptMsgType, 3), end()...)
	packet := buildPacket(main)
	copy(packet[ciaddrOff:], []byte{192, 0, 2, 10})
	copy(packet[yiaddrOff:], []byte{192, 0, 2, 20})
	copy(packet[giaddrOff:], []byte{192, 0, 2, 1})

	binding, err := ParseBinding(packet)
	if err != nil {
		t.Fatalf("ParseBinding: %v", err)
	}
	if !bytes.Equal(binding.HardwareAddr, testMAC) {
		t.Fatalf("hardware addr = %v, want %v", binding.HardwareAddr, testMAC)
	}
	if binding.MessageType != 3 {
		t.Fatalf("message type = %d, want 3", binding.MessageType)
	}
	if !bytes.Equal(binding.YourIP, []byte{192, 0, 2, 20}) {
		t.Fatalf("your ip = %v", binding.YourIP)
	}
	if !bytes.Equal(binding.RelayIP, []byte{192, 0, 2, 1}) {
		t.Fatalf("relay ip = %v", binding.RelayIP)
	}
	if len(binding.XID) != 4 {
		t.Fatalf("xid length = %d, want 4", len(binding.XID))
	}
}

func TestParseBindingFailuresReturnNil(t *testing.T) {
	cases := map[string][]byte{
		"truncated header": make([]byte, bootpMinLen-1),
		"bad cookie": func() []byte {
			packet := buildPacket(append(tlv(OptMsgType, 1), end()...))
			packet[mainOptionsStart] = 0
			return packet
		}(),
		"cross-area slice": func() []byte {
			return buildPacket([]byte{OptSubnet, 10, 1, 2, 3, 4})
		}(),
		"bad message type": func() []byte {
			return buildPacket(append(tlv(OptMsgType, 42, 7), end()...))
		}(),
		"bad address list": func() []byte {
			return buildPacket(append(tlv(OptRouter, 1, 2, 3), end()...))
		}(),
	}

	for name, packet := range cases {
		t.Run(name, func(t *testing.T) {
			binding, err := ParseBinding(packet)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if binding != nil {
				t.Fatalf("binding = %+v, want nil on failure", binding)
			}
		})
	}
}

func TestBadHardwareLengthRejected(t *testing.T) {
	packet := buildPacket(append(tlv(OptMsgType, 1), end()...))
	packet[hlenOffset] = chaddrMax + 1

	binding, err := ParseBinding(packet)
	if !errors.Is(err, ErrBadHardwareLen) || binding != nil {
		t.Fatalf("binding=%v err=%v, want nil/ErrBadHardwareLen", binding, err)
	}

	packet[hlenOffset] = 0
	if binding, err := ParseBinding(packet); !errors.Is(err, ErrBadHardwareLen) || binding != nil {
		t.Fatalf("binding=%v err=%v, want nil/ErrBadHardwareLen", binding, err)
	}
}

func TestInspectionAndBusinessUseSameNormalization(t *testing.T) {
	// The same Options value produced by ParseOptions is what ParseBinding
	// validates and exposes; mutate the packet after parsing to prove the
	// binding's view is the independent normalized view.
	packet := buildPacket(append(
		append(tlv(OptOverload, overloadFileBit), tlv(OptSubnet, 172, 16)...),
		end()...))
	fillField(packet, fileStart, tlv(OptSubnet, 0, 1))

	options, err := ParseOptions(packet)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if err := options.Validate(); err != nil {
		t.Fatalf("inspection validation: %v", err)
	}

	binding, err := ParseBinding(packet)
	if err != nil {
		t.Fatalf("ParseBinding: %v", err)
	}
	if !bytes.Equal(binding.Options.Raw(OptSubnet), options.Raw(OptSubnet)) {
		t.Fatal("business layer options differ from inspection layer options")
	}

	for i := fileStart; i < fileStart+fileLen; i++ {
		packet[i] = 0
	}
	if got := binding.Options.Raw(OptSubnet); !bytes.Equal(got, []byte{172, 16, 0, 1}) {
		t.Fatalf("business view changed after packet mutation: %v", got)
	}
}
