package netsec

import (
	"bytes"
	"errors"
	"testing"
)

var testMAC = []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

func tlv(code uint8, value ...byte) []byte {
	out := make([]byte, 0, 2+len(value))
	out = append(out, code, byte(len(value)))
	return append(out, value...)
}

func pad() []byte { return []byte{OptPad} }
func end() []byte { return []byte{OptEnd} }

// buildPacket creates a minimum-size BOOTP packet with a valid cookie and the
// given main option area. The sname/file fields start out zero-filled (PADs).
func buildPacket(mainOpts []byte) []byte {
	packet := make([]byte, mainOptionsStart+magicCookieLen+len(mainOpts))
	packet[hlenOffset] = byte(len(testMAC))
	copy(packet[chaddrOff:], testMAC)
	copy(packet[mainOptionsStart:], magicCookie[:])
	copy(packet[mainOptionsStart+magicCookieLen:], mainOpts)
	return packet
}

func fillField(packet []byte, start int, payload []byte) {
	copy(packet[start:], payload)
}

func TestParseConcatenatesRepeatedOptions(t *testing.T) {
	main := bytes.NewBuffer(nil)
	main.Write(tlv(OptMsgType, 1))
	main.Write(tlv(OptSubnet, 255, 255))
	main.Write(tlv(OptSubnet, 255, 0))
	main.Write(tlv(OptRouter, 10, 0, 0, 1))
	main.Write(tlv(OptRouter, 10, 0, 0, 2))
	main.Write(end())

	options, err := ParseOptions(buildPacket(main.Bytes()))
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if got, want := options.Raw(OptSubnet), []byte{255, 255, 255, 0}; !bytes.Equal(got, want) {
		t.Fatalf("subnet concat = %v, want %v", got, want)
	}
	if msgType, ok := options.Byte(OptMsgType); !ok || msgType != 1 {
		t.Fatalf("message type = %d/%v, want 1/true", msgType, ok)
	}
	routers, ok := options.IPv4List(OptRouter)
	if !ok || len(routers) != 2 {
		t.Fatalf("router list = %v (ok=%v), want 2 addresses", routers, ok)
	}
	if err := options.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestPADAndENDOnlyAffectOwnArea(t *testing.T) {
	main := bytes.NewBuffer(nil)
	main.Write(pad())
	main.Write(pad())
	main.Write(tlv(OptMsgType, 1))
	main.Write(end())
	main.Write(tlv(OptSubnet, 1, 2, 3, 4)) // must be ignored: END already hit

	options, err := ParseOptions(buildPacket(main.Bytes()))
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if !options.Has(OptMsgType) || options.Has(OptSubnet) {
		t.Fatalf("END did not terminate main area correctly: %v", options)
	}

	// Implicit end: an area whose bytes run out without END is still valid.
	packet := buildPacket(tlv(OptMsgType, 3))
	options, err = ParseOptions(packet)
	if err != nil {
		t.Fatalf("implicit area end rejected: %v", err)
	}
	if msgType, _ := options.Byte(OptMsgType); msgType != 3 {
		t.Fatalf("message type = %d, want 3", msgType)
	}
}

func TestOverloadOrderMainFileSname(t *testing.T) {
	main := bytes.NewBuffer(nil)
	main.Write(tlv(OptOverload, overloadFileBit|overloadSnameBit))
	main.Write(tlv(60, 'a'))
	main.Write(end())

	packet := buildPacket(main.Bytes())
	filePayload := append(append(tlv(60, 'b'), pad()...), end()...)
	filePayload = append(filePayload, tlv(60, 'z')...) // ignored after file END
	fillField(packet, fileStart, filePayload)
	fillField(packet, snameStart, tlv(60, 'c'))

	options, err := ParseOptions(packet)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if got, want := options.Raw(60), []byte{'a', 'b', 'c'}; !bytes.Equal(got, want) {
		t.Fatalf("overload concat order = %q, want %q", got, want)
	}
}

func TestOverloadBitsSelectAreas(t *testing.T) {
	// Only the sname bit is set: file content must stay untouched.
	main := append(tlv(OptOverload, overloadSnameBit), end()...)
	packet := buildPacket(main)
	fillField(packet, fileStart, tlv(60, 'f'))
	fillField(packet, snameStart, tlv(61, 's'))

	options, err := ParseOptions(packet)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if options.Has(60) || !options.Has(61) {
		t.Fatalf("area selection wrong: file parsed=%v sname parsed=%v",
			options.Has(60), options.Has(61))
	}

	// No overload at all: legacy sname/file fields are not option areas.
	packet = buildPacket(append(tlv(OptMsgType, 1), end()...))
	fillField(packet, fileStart, tlv(60, 'x'))
	options, err = ParseOptions(packet)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if options.Has(60) {
		t.Fatal("legacy file field was parsed without an overload option")
	}
}

func TestDuplicateOverloadRejected(t *testing.T) {
	cases := map[string][]byte{
		"duplicate different value": {
			byte(OptOverload), 1, 1,
			byte(OptOverload), 1, 2,
		},
		"duplicate same value": {
			byte(OptOverload), 1, 1,
			byte(OptOverload), 1, 1,
		},
		"bad length": {
			byte(OptOverload), 2, 1, 0,
		},
		"out of range value": {
			byte(OptOverload), 1, 9,
		},
	}
	for name, area := range cases {
		t.Run(name, func(t *testing.T) {
			options, err := ParseOptions(buildPacket(area))
			if err == nil || !errors.Is(err, ErrOverloadValue) {
				t.Fatalf("err = %v, want ErrOverloadValue", err)
			}
			if options != nil {
				t.Fatalf("options = %v, want nil on failure", options)
			}
			if binding, err := ParseBinding(buildPacket(area)); err == nil || binding != nil {
				t.Fatalf("binding = %v, err = %v; want nil binding", binding, err)
			}
		})
	}
}

func TestNestedOverloadRejected(t *testing.T) {
	main := append(tlv(OptOverload, overloadFileBit), end()...)
	packet := buildPacket(main)
	fillField(packet, fileStart, tlv(OptOverload, 1))

	options, err := ParseOptions(packet)
	if !errors.Is(err, ErrNestedOverload) {
		t.Fatalf("err = %v, want ErrNestedOverload", err)
	}
	if options != nil {
		t.Fatalf("options = %v, want nil", options)
	}
}

func TestMaliciousCrossAreaSlices(t *testing.T) {
	t.Run("main area length overruns", func(t *testing.T) {
		// Option 1 claims 10 value bytes; only 4 are present in the area.
		area := []byte{OptSubnet, 10, 1, 2, 3, 4}
		options, err := ParseOptions(buildPacket(area))
		if !errors.Is(err, ErrTruncated) || options != nil {
			t.Fatalf("options=%v err=%v, want nil/ErrTruncated", options, err)
		}
	})

	t.Run("main area ends on length byte", func(t *testing.T) {
		area := []byte{OptSubnet}
		options, err := ParseOptions(buildPacket(area))
		if !errors.Is(err, ErrTruncated) || options != nil {
			t.Fatalf("options=%v err=%v, want nil/ErrTruncated", options, err)
		}
	})

	t.Run("file area length cannot reach sname", func(t *testing.T) {
		packet := buildPacket(append(tlv(OptOverload, overloadFileBit), end()...))
		// 128-byte file field: code+len claim 200 bytes but only 126 remain.
		payload := append([]byte{OptSubnet, 200}, make([]byte, 126)...)
		fillField(packet, fileStart, payload)

		options, err := ParseOptions(packet)
		if !errors.Is(err, ErrTruncated) || options != nil {
			t.Fatalf("options=%v err=%v, want nil/ErrTruncated", options, err)
		}
		if binding, err := ParseBinding(packet); err == nil || binding != nil {
			t.Fatalf("binding=%v err=%v, want nil binding on cross-area slice", binding, err)
		}
	})

	t.Run("sname area length cannot leave field", func(t *testing.T) {
		packet := buildPacket(append(tlv(OptOverload, overloadSnameBit), end()...))
		payload := append([]byte{OptDNS, 100}, make([]byte, 62)...)
		fillField(packet, snameStart, payload)

		options, err := ParseOptions(packet)
		if !errors.Is(err, ErrTruncated) || options != nil {
			t.Fatalf("options=%v err=%v, want nil/ErrTruncated", options, err)
		}
	})
}

func TestFixedLengthCheckRunsAfterConcatenation(t *testing.T) {
	// RFC 3396: a 4-byte subnet mask split over main + file areas is valid.
	packet := buildPacket(append(
		append(tlv(OptOverload, overloadFileBit), tlv(OptSubnet, 172, 16)...),
		end()...))
	fillField(packet, fileStart, tlv(OptSubnet, 0, 1))

	options, err := ParseOptions(packet)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if got, want := options.Raw(OptSubnet), []byte{172, 16, 0, 1}; !bytes.Equal(got, want) {
		t.Fatalf("cross-area concat = %v, want %v", got, want)
	}
	if err := options.Validate(); err != nil {
		t.Fatalf("Validate after concat: %v", err)
	}

	// Same trick with a 3-byte total must fail fixed-length validation, and
	// the business layer must receive no partial binding.
	evil := buildPacket(append(
		append(tlv(OptOverload, overloadFileBit), tlv(OptSubnet, 10)...),
		end()...))
	fillField(evil, fileStart, tlv(OptSubnet, 0, 0))
	if _, err := ParseOptions(evil); err != nil {
		t.Fatalf("structural parse should pass, got %v", err)
	}
	binding, err := ParseBinding(evil)
	if !errors.Is(err, ErrOptionLength) || binding != nil {
		t.Fatalf("binding=%v err=%v, want nil/ErrOptionLength", binding, err)
	}
}

func TestNormalizedResultDoesNotAliasPacket(t *testing.T) {
	packet := buildPacket(append(tlv(OptSubnet, 1, 2, 3, 4), end()...))
	options, err := ParseOptions(packet)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}

	packet[mainOptionsStart+magicCookieLen+2] = 99
	if got := options.Raw(OptSubnet); !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("normalized options alias packet memory: %v", got)
	}

	handed := options.Raw(OptSubnet)
	handed[0] = 77
	if got := options.Raw(OptSubnet); got[0] != 1 {
		t.Fatalf("Raw() mutated normalized storage: %v", got)
	}
}
