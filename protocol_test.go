package netsec

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func makeBinaryFrame(magic, opcode byte, key, extras, value []byte) []byte {
	body := make([]byte, 0, len(extras)+len(key)+len(value))
	body = append(body, extras...)
	body = append(body, key...)
	body = append(body, value...)
	header := make([]byte, binaryHeaderLength)
	header[0] = magic
	header[1] = opcode
	binary.BigEndian.PutUint16(header[2:4], uint16(len(key)))
	header[4] = byte(len(extras))
	binary.BigEndian.PutUint32(header[8:12], uint32(len(body)))
	binary.BigEndian.PutUint32(header[12:16], 0x01020304)
	return append(header, body...)
}

func TestProtocolLockedToTextThenBinaryProbeRejected(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	s := n.NewSession(strings.NewReader("version\r\n" + strings.Repeat("\x80", 4)))
	req, err := s.ReadRequest()
	if err != nil || req.Protocol != ProtocolText {
		t.Fatalf("first request = %v, %v", req, err)
	}
	if s.Protocol() != ProtocolText {
		t.Fatalf("protocol = %v", s.Protocol())
	}
	if _, err := s.ReadRequest(); !errors.Is(err, ErrProtocolSwitch) {
		t.Fatalf("binary probe error = %v, want ErrProtocolSwitch", err)
	}
}

func TestBinaryDisabledLocksProtocolAndConsumesProbe(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	frame := makeBinaryFrame(binaryRequestMagic, 0x00, []byte("alpha"), nil, nil)
	s := n.NewSession(bytes.NewReader(append(frame, []byte("version\r\n")...)))
	if _, err := s.ReadRequest(); !errors.Is(err, ErrBinaryUnsupported) {
		t.Fatalf("binary probe error = %v, want ErrBinaryUnsupported", err)
	}
	if s.Protocol() != ProtocolBinary {
		t.Fatalf("protocol = %v, want binary", s.Protocol())
	}
	if _, err := s.ReadRequest(); !errors.Is(err, ErrProtocolSwitch) {
		t.Fatalf("text fallback error = %v, want ErrProtocolSwitch", err)
	}
}

func TestBareBinaryMagicIsRejectedAsBinary(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	s := n.NewSession(bytes.NewReader([]byte{binaryRequestMagic}))
	if _, err := s.ReadRequest(); !errors.Is(err, ErrBinaryUnsupported) {
		t.Fatalf("bare binary magic error = %v, want ErrBinaryUnsupported", err)
	}
	if s.Protocol() != ProtocolBinary {
		t.Fatalf("protocol = %v, want binary", s.Protocol())
	}
}

func TestBinaryThenTextRejected(t *testing.T) {
	cfg := Config{AllowBinary: true}
	n := mustNormalizer(t, "t1:", &cfg)
	frame := makeBinaryFrame(binaryRequestMagic, 0x00, []byte("alpha"), nil, nil)
	s := n.NewSession(bytes.NewReader(append(frame, []byte("version\r\n")...)))
	req, err := s.ReadRequest()
	if err != nil || req.Protocol != ProtocolBinary {
		t.Fatalf("binary request = %v, %v", req, err)
	}
	if _, err := s.ReadRequest(); !errors.Is(err, ErrProtocolSwitch) {
		t.Fatalf("text switch error = %v, want ErrProtocolSwitch", err)
	}
}

func TestBinaryGetKeyIsPrefixed(t *testing.T) {
	cfg := Config{AllowBinary: true}
	n := mustNormalizer(t, "t1:", &cfg)
	frame := makeBinaryFrame(binaryRequestMagic, 0x00, []byte("alpha"), nil, nil)
	req, err := n.NormalizeBinary(frame)
	if err != nil {
		t.Fatal(err)
	}
	if req.Protocol != ProtocolBinary || req.Command != "get" {
		t.Fatalf("request = %v/%q", req.Protocol, req.Command)
	}
	if got := binary.BigEndian.Uint16(req.Bytes[2:4]); got != uint16(len("t1:alpha")) {
		t.Fatalf("normalized key length = %d", got)
	}
	if string(req.Bytes[24:]) != "t1:alpha" {
		t.Fatalf("normalized body = %q", req.Bytes[24:])
	}
}

func TestBinaryStoragePreservesValueAndRejectsBadExtras(t *testing.T) {
	cfg := Config{AllowBinary: true}
	n := mustNormalizer(t, "t1:", &cfg)
	frame := makeBinaryFrame(binaryRequestMagic, 0x01, []byte("key"), make([]byte, 8), []byte("value"))
	req, err := n.NormalizeBinary(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(req.Bytes, []byte("value")) {
		t.Fatalf("binary value was not preserved: %q", req.Bytes)
	}
	if got := binary.BigEndian.Uint16(req.Bytes[2:4]); got != uint16(len("t1:key")) {
		t.Fatalf("binary storage key length = %d", got)
	}

	badExtras := makeBinaryFrame(binaryRequestMagic, 0x01, []byte("key"), nil, []byte("value"))
	if _, err := n.NormalizeBinary(badExtras); !errors.Is(err, ErrMalformedRequest) {
		t.Fatalf("bad binary extras error = %v", err)
	}
}

func TestBinaryOverlongKeyRejected(t *testing.T) {
	cfg := Config{AllowBinary: true}
	n := mustNormalizer(t, "t1:", &cfg)
	key := bytes.Repeat([]byte("k"), MaxProtocolKeyLength-len("t1:")+1)
	frame := makeBinaryFrame(binaryRequestMagic, 0x00, key, nil, nil)
	if _, err := n.NormalizeBinary(frame); !errors.Is(err, ErrKeyTooLong) {
		t.Fatalf("overlong binary key error = %v", err)
	}
}

func TestBinaryResponseKeyFiltering(t *testing.T) {
	cfg := Config{AllowBinary: true}
	n := mustNormalizer(t, "t1:", &cfg)
	req := &Request{Protocol: ProtocolBinary, ResponseKind: ResponseSingle}
	tenant := makeBinaryFrame(binaryResponseMagic, 0x0c, []byte("t1:alpha"), make([]byte, 4), []byte("hi"))
	got, err := n.ReadResponseFrom(bytes.NewReader(tenant), req)
	if err != nil {
		t.Fatal(err)
	}
	keyStart := binaryHeaderLength + 4
	if string(got[keyStart:keyStart+len("alpha")]) != "alpha" {
		t.Fatalf("binary response key = %q", got[keyStart:])
	}
	if binary.BigEndian.Uint16(got[2:4]) != uint16(len("alpha")) {
		t.Fatalf("binary response key length was not rewritten")
	}

	foreign := makeBinaryFrame(binaryResponseMagic, 0x0c, []byte("t2:secret"), make([]byte, 4), []byte("secret"))
	got, err = n.ReadResponseFrom(bytes.NewReader(foreign), req)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("foreign binary response was emitted: %q", got)
	}
}

func TestSessionPipelinesTextWithoutProtocolRelock(t *testing.T) {
	n := mustNormalizer(t, "t1:", nil)
	raw := "set k 0 0 1\r\nx\r\n" +
		"set t2:evil 0 0 1\r\nx\r\n" +
		"get k\r\n"
	br := bufio.NewReader(strings.NewReader(raw))
	s := n.NewSession(br)
	want := []string{
		"set t1:k 0 0 1\r\nx\r\n",
		"set t1:t2:evil 0 0 1\r\nx\r\n",
		"get t1:k\r\n",
	}
	for i, wantBytes := range want {
		req, err := s.ReadRequest()
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if got := string(req.Bytes); got != wantBytes {
			t.Fatalf("request %d = %q, want %q", i, got, wantBytes)
		}
	}
	if _, err := s.ReadRequest(); err == nil {
		t.Fatal("expected EOF")
	}
}
