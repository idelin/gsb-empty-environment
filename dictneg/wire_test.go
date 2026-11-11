package dictneg

import (
	"errors"
	"testing"
)

func TestEventWireRoundTrip(t *testing.T) {
	d := SumDigest([]byte("dict"))
	cases := []Event{
		{Kind: EventInstall, Epoch: 7, Digest: d, Payload: []byte("dict")},
		{Kind: EventAck, Epoch: 7, Digest: d},
		{Kind: EventNack, Epoch: 8, Digest: d},
		{Kind: EventEnable, Epoch: 9, Digest: d},
	}
	for _, want := range cases {
		data, err := want.MarshalBinary()
		if err != nil {
			t.Fatalf("marshal %s: %v", want.Kind, err)
		}
		var got Event
		if err := got.UnmarshalBinary(data); err != nil {
			t.Fatalf("unmarshal %s: %v", want.Kind, err)
		}
		if got.Kind != want.Kind || got.Epoch != want.Epoch || got.Digest != want.Digest {
			t.Fatalf("round trip = %+v, want %+v", got, want)
		}
		if string(got.Payload) != string(want.Payload) {
			t.Fatalf("payload = %q, want %q", got.Payload, want.Payload)
		}
	}
}

func TestEventWireRejectsInvalid(t *testing.T) {
	// 非 Install 事件不得携带载荷。
	bad := Event{Kind: EventEnable, Epoch: 1, Digest: SumDigest([]byte("x")), Payload: []byte("x")}
	if _, err := bad.MarshalBinary(); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("enable with payload: %v", err)
	}
	// 零值事件非法。
	if _, err := (Event{}).MarshalBinary(); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("zero event: %v", err)
	}
	// 截断与篡改的报文必须被拒绝。
	good, err := (Event{Kind: EventAck, Epoch: 1, Digest: SumDigest([]byte("y"))}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var ev Event
	if err := ev.UnmarshalBinary(good[:10]); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("truncated: %v", err)
	}
	tampered := append([]byte(nil), good...)
	tampered[0] = 'X'
	if err := ev.UnmarshalBinary(tampered); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("bad magic: %v", err)
	}
	tampered = append([]byte(nil), good...)
	tampered[5] = 99
	if err := ev.UnmarshalBinary(tampered); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("bad kind: %v", err)
	}
}

func TestFrameWireRoundTrip(t *testing.T) {
	want := Frame{Digest: SumDigest([]byte("d")), Seq: 42, Payload: []byte("compressed-bytes")}
	data, err := want.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var got Frame
	if err := got.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if got.Digest != want.Digest || got.Seq != want.Seq || string(got.Payload) != string(want.Payload) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if err := got.UnmarshalBinary(data[:len(data)-1]); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("truncated frame: %v", err)
	}
}
