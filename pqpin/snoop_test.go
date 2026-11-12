package pqpin

import (
	"bytes"
	"crypto/tls"
	"testing"
)

// buildServerHelloRecord assures a minimal plaintext TLS 1.3 record carrying
// a ServerHello (or HRR) with a key_share extension selecting group.
func buildServerHelloRecord(group uint16, hrr bool) []byte {
	random := make([]byte, 32)
	if hrr {
		copy(random, helloRetryRequestRandom[:])
	}

	var keyShareExt []byte
	keyShareExt = appendUint16(keyShareExt, extKeyShare)
	if hrr {
		// HRR: extension data is just the selected group (2 bytes).
		keyShareExt = appendUint16(keyShareExt, 2)
		keyShareExt = appendUint16(keyShareExt, group)
	} else {
		// ServerHello: group + uint16-length-prefixed key exchange.
		keyExchange := []byte{0xAA, 0xBB, 0xCC}
		keyShareExt = appendUint16(keyShareExt, uint16(2+2+len(keyExchange)))
		keyShareExt = appendUint16(keyShareExt, group)
		keyShareExt = appendUint16(keyShareExt, uint16(len(keyExchange)))
		keyShareExt = append(keyShareExt, keyExchange...)
	}

	var sh bytes.Buffer
	sh.Write([]byte{0x03, 0x03}) // legacy_version
	sh.Write(random)
	sh.WriteByte(0)              // empty legacy_session_id
	sh.Write([]byte{0x13, 0x01}) // TLS_AES_128_GCM_SHA256
	sh.WriteByte(0)              // null compression
	sh.Write(appendUint16(nil, uint16(len(keyShareExt))))
	sh.Write(keyShareExt)

	msg := sh.Bytes()
	record := []byte{recordHandshake, 0x03, 0x01}
	record = appendUint16(record, uint16(4+len(msg)))
	record = append(record, handshakeServerHello)
	record = append(record, byte(len(msg)>>16), byte(len(msg)>>8), byte(len(msg)))
	record = append(record, msg...)
	return record
}

func appendUint16(b []byte, v uint16) []byte {
	return append(b, byte(v>>8), byte(v))
}

func TestParseServerHello(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hrr     bool
		group   uint16
		wantHRR bool
	}{
		{"final serverhello hybrid", false, hybrid, false},
		{"final serverhello classic", false, classic, false},
		{"hrr classic", true, classic, true},
		{"hrr hybrid", true, hybrid, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := buildServerHelloRecord(tc.group, tc.hrr)
			s := &snoopConn{}
			s.feed(record)
			if tc.wantHRR {
				if !s.hadHRR || s.hrrGroup != tc.group {
					t.Fatalf("HRR not parsed: %+v", s)
				}
				if s.done {
					t.Fatalf("snoop must keep listening after HRR")
				}
			} else {
				if s.negotiatedGroup != tc.group || !s.done {
					t.Fatalf("ServerHello not parsed: %+v", s)
				}
			}
		})
	}
}

func TestSnoopHRRThenFinalServerHello(t *testing.T) {
	hrr := buildServerHelloRecord(classic, true)
	final := buildServerHelloRecord(hybrid, false)

	var callbackGroup uint16
	s := newSnoopConn(nil, func(group uint16) error {
		callbackGroup = group
		return nil
	})

	// Feed byte by byte to exercise the partial-record buffering.
	for _, b := range hrr {
		s.feed([]byte{b})
	}
	if !s.hadHRR || s.hrrGroup != classic || callbackGroup != classic {
		t.Fatalf("HRR callback did not fire correctly: %+v", s)
	}
	for _, b := range final {
		s.feed([]byte{b})
	}
	if !s.done || s.negotiatedGroup != hybrid {
		t.Fatalf("final ServerHello not captured: %+v", s)
	}
}

func TestSnoopIgnoresOtherRecords(t *testing.T) {
	record := []byte{21 /* alert */, 0x03, 0x03, 0x00, 0x02, 0x02, 0x28}
	s := &snoopConn{}
	s.feed(record)
	if s.done || s.negotiatedGroup != 0 {
		t.Fatalf("non-handshake record must be ignored: %+v", s)
	}
}

func TestHybridConstantMatchesStdlib(t *testing.T) {
	if uint16(tls.X25519MLKEM768) != 0x11EC {
		t.Fatalf("unexpected hybrid curve id: %#x", tls.X25519MLKEM768)
	}
}
