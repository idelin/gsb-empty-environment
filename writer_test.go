package netsec

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestWriteFrameRoundTrip(t *testing.T) {
	payloads := [][]byte{
		[]byte("MSH|^~\\&|A|B|||20261116|||ADT^A01|1|P|2.5" + string(CR)),
		[]byte("PID|1||12345^^^MR||DOE^JOHN" + string(CR)),
		{},
	}
	var wire bytes.Buffer
	w := NewFrameWriter(&wire)
	for _, payload := range payloads {
		if err := w.WriteFrame(payload); err != nil {
			t.Fatalf("WriteFrame(%q): %v", payload, err)
		}
	}

	p := NewParser(0)
	if err := p.Feed(wire.Bytes()); err != nil {
		t.Fatalf("parser rejected well-formed output: %v", err)
	}
	for _, want := range payloads {
		got, err := p.Next()
		if err != nil {
			t.Fatalf("unexpected parser event: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("round trip mismatch:\n got %q\nwant %q", got, want)
		}
	}
	if ev, _ := p.Next(); ev != nil {
		t.Fatalf("unexpected trailing event: %#v", ev)
	}
	if err := p.End(); !errors.Is(err, io.EOF) {
		t.Fatalf("want clean EOF, got %v", err)
	}
}

func TestWriteRejectsFramingBytes(t *testing.T) {
	cases := map[string][]byte{
		"VT inside field":    []byte("MSH|bad" + string(VT) + "value" + string(CR)),
		"FS inside field":    []byte("MSH|bad" + string(FS) + "value" + string(CR)),
		"leading VT":         {VT, 'a', CR},
		"trailing FS before": []byte("abc" + string(FS)),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			err := NewFrameWriter(&buf).WriteFrame(payload)
			if !errors.Is(err, ErrFramingByte) {
				t.Fatalf("want ErrFramingByte, got %v", err)
			}
			if buf.Len() != 0 {
				t.Fatalf("rejected payload must not hit the wire, wrote % X", buf.Bytes())
			}
		})
	}
}

func TestWriteCRMustTerminateSegment(t *testing.T) {
	valid := [][]byte{
		[]byte("MSH|a" + string(CR) + "PID|b" + string(CR)),
		[]byte("only segment" + string(CR)),
		[]byte("no segment terminator at all"),
		{},
	}
	for _, payload := range valid {
		if err := ValidatePayload(payload); err != nil {
			t.Fatalf("payload %q wrongly rejected: %v", payload, err)
		}
	}

	invalid := [][]byte{
		[]byte("MSH|a" + string(CR) + "lowercase" + string(CR)), // CR then lowercase segment id
		[]byte("MSH|a" + string(CR) + "|pipefirst" + string(CR)),
		[]byte("mid" + string(CR) + "1digit" + string(CR)),
	}
	for _, payload := range invalid {
		err := ValidatePayload(payload)
		if !errors.Is(err, ErrCRPosition) {
			t.Fatalf("payload %q: want ErrCRPosition, got %v", payload, err)
		}
	}
}

func TestEndToEndSmugglingDefense(t *testing.T) {
	// A decoded field carrying framing bytes is rejected before storage and
	// before any outbound frame is produced.
	raw := []byte(`allergy \X0B\ peanuts \X1C\0D\X0B\`)
	decoded, err := UnescapeField(raw)
	if !errors.Is(err, ErrEscape) {
		t.Fatalf("unescape must reject decoded framing bytes, got frame=%q err=%v", decoded, err)
	}

	// Literal framing bytes in an outbound message are refused, never escaped.
	var buf bytes.Buffer
	err = NewFrameWriter(&buf).WriteFrame([]byte("MSH|x" + string(VT) + string(CR)))
	if !errors.Is(err, ErrFramingByte) || buf.Len() != 0 {
		t.Fatalf("writer defense failed: err=%v bytes=% X", err, buf.Bytes())
	}
}
