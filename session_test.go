package bridge

import (
	"errors"
	"testing"
)

func TestEpochHotUpdateMidFrame(t *testing.T) {
	v1 := replayV1()
	sess, err := Negotiate(v1, replayV2())
	if err != nil {
		t.Fatal(err)
	}
	pair2, err := CompilePair(v1, replayV2b())
	if err != nil {
		t.Fatal(err)
	}
	ep := NewEndpoint(sess, AtoB, 1024)

	msg := NewMessage()
	msg.Values[1] = IntVal(9)
	msg.Values[2] = StrVal("bo")
	msg.Values[3] = IntVal(5)
	frame := EncodeFrame(msg.Encode())
	half := len(frame) / 2

	// Frame starts in epoch 0.
	out, err := ep.Feed(frame[:half])
	if err != nil || len(out) != 0 {
		t.Fatalf("first half: out=%d err=%v", len(out), err)
	}
	// Hot update lands while the frame is in flight.
	sess.Publish(pair2)
	if sess.Epoch() != 1 {
		t.Fatalf("epoch = %d, want 1", sess.Epoch())
	}
	// ...but the in-flight frame still converts under the old plan.
	out, err = ep.Feed(frame[half:])
	if err != nil || len(out) != 1 {
		t.Fatalf("second half: out=%d err=%v", len(out), err)
	}
	old, err := DecodeMessage(replayV2(), framePayload(out[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := old.Values[2]; !ok {
		t.Fatal("in-flight frame must use the old plan: field 2 should be direct")
	}

	// The next frame uses the new plan: field 2 moves to the extension blob.
	out, err = ep.Feed(frame)
	if err != nil || len(out) != 1 {
		t.Fatalf("new epoch frame: out=%d err=%v", len(out), err)
	}
	newM, err := DecodeMessage(replayV2b(), framePayload(out[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := newM.Values[2]; ok {
		t.Fatal("new epoch frame must use the new plan: field 2 should be extended")
	}
	found := false
	for _, f := range flattenUnknown(newM) {
		if f.ID == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("field 2 not preserved in extension blob under new plan")
	}
}

func TestNoHalfConvertedFramePublished(t *testing.T) {
	sess, err := Negotiate(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	ep := NewEndpoint(sess, AtoB, 1024)

	// Field 1 carries a string but the schema says int: bad data.
	bad := appendRawField(nil, RawField{ID: 1, Type: TyString, Data: []byte("x")})
	out, err := ep.Feed(EncodeFrame(bad))
	if !errors.Is(err, ErrBadData) {
		t.Fatalf("err = %v, want ErrBadData", err)
	}
	if len(out) != 0 {
		t.Fatalf("published %d frames from a failed conversion", len(out))
	}

	// The endpoint is poisoned: even valid input is rejected afterwards.
	good := NewMessage()
	good.Values[1] = IntVal(1)
	if _, err := ep.Feed(EncodeFrame(good.Encode())); err == nil {
		t.Fatal("poisoned endpoint accepted further input")
	}
}

func TestBidirectionalEndpoints(t *testing.T) {
	sess, err := Negotiate(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	forward := NewEndpoint(sess, AtoB, 1024)
	backward := NewEndpoint(sess, BtoA, 1024)

	out, err := forward.Feed(EncodeFrame(sampleV1Message().Encode()))
	if err != nil || len(out) != 1 {
		t.Fatalf("forward: out=%d err=%v", len(out), err)
	}
	back, err := backward.Feed(out[0])
	if err != nil || len(back) != 1 {
		t.Fatalf("backward: out=%d err=%v", len(back), err)
	}
	m, err := DecodeMessage(replayV1(), framePayload(back[0]))
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range sampleV1Message().Values {
		if !m.Values[id].Equal(want) {
			t.Errorf("field %d = %v, want %v", id, m.Values[id], want)
		}
	}
}
