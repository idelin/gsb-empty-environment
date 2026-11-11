package proxy

import (
	"bytes"
	"encoding/binary"
	"io"
	"reflect"
	"testing"
	"time"

	"example.org/offlinecli/hpack"
)

func settingsPayload(tableSize uint32) []byte {
	var p [6]byte
	binary.BigEndian.PutUint16(p[0:], SettingHeaderTableSize)
	binary.BigEndian.PutUint32(p[2:], tableSize)
	return p[:]
}

func TestReceiverDecodesInterleavedControlAndRequests(t *testing.T) {
	enc := hpack.NewEncoder(4096)
	fields1 := []HeaderField{hf(":method", "GET"), hf("x-tenant", "alpha")}
	fields2 := []HeaderField{hf(":method", "POST"), hf("x-tenant", "beta")}

	var stream []byte
	stream = appendFrame(stream, FrameSettings, 0, 0, settingsPayload(1024))
	stream = appendFrame(stream, FrameHeaders, FlagEndHeaders|FlagEndStream, 1, enc.Encode(nil, fields1))
	var pingData [8]byte
	copy(pingData[:], "deadbeef")
	stream = appendFrame(stream, FramePing, 0, 0, pingData[:])
	stream = appendFrame(stream, FrameSettings, FlagAck, 0, nil)
	stream = appendFrame(stream, FrameHeaders, FlagEndHeaders, 3, enc.Encode(nil, fields2))

	var gotSettings uint32
	var settingsSeen, ackSeen, pingSeen bool
	rc := NewReceiver(bytes.NewReader(stream), 4096)
	rc.OnPeerSettings = func(size uint32, present bool) {
		settingsSeen = present
		gotSettings = size
	}
	rc.OnSettingsAck = func() { ackSeen = true }
	rc.OnPing = func(data [8]byte, ack bool) {
		if data == pingData && !ack {
			pingSeen = true
		}
	}

	msg1, err := rc.Next()
	if err != nil {
		t.Fatalf("next 1: %v", err)
	}
	msg2, err := rc.Next()
	if err != nil {
		t.Fatalf("next 2: %v", err)
	}
	if msg1.StreamID != 1 || !msg1.EndStream || !reflect.DeepEqual(msg1.Fields, fields1) {
		t.Fatalf("msg1: %+v", msg1)
	}
	if msg2.StreamID != 3 || msg2.EndStream || !reflect.DeepEqual(msg2.Fields, fields2) {
		t.Fatalf("msg2: %+v", msg2)
	}
	if !settingsSeen || gotSettings != 1024 {
		t.Fatalf("settings callback: seen %v size %d", settingsSeen, gotSettings)
	}
	if !ackSeen || !pingSeen {
		t.Fatalf("ack %v ping %v", ackSeen, pingSeen)
	}
}

func TestReceiverRejectsFrameInterleavedIntoHeaderBlock(t *testing.T) {
	enc := hpack.NewEncoder(4096)
	block := enc.Encode(nil, []HeaderField{hf("x-pad", string(make([]byte, 64)))})

	var stream []byte
	// HEADERS without END_HEADERS, then a SETTINGS frame: protocol error.
	stream = appendFrame(stream, FrameHeaders, 0, 1, block[:4])
	stream = appendFrame(stream, FrameSettings, 0, 0, settingsPayload(128))

	rc := NewReceiver(bytes.NewReader(stream), 4096)
	_, err := rc.Next()
	ce, ok := err.(ConnectionError)
	if !ok || ce.Code != ErrCodeProtocol {
		t.Fatalf("err = %v, want ConnectionError(PROTOCOL_ERROR)", err)
	}
}

func TestReceiverWaitsForHalfWrittenBlock(t *testing.T) {
	enc := hpack.NewEncoder(4096)
	fields := []HeaderField{hf(":method", "GET"), hf("x-pad", "0123456789abcdef")}
	block := enc.Encode(nil, fields)
	split := len(block) / 2

	pr, pw := io.Pipe()
	rc := NewReceiver(pr, 4096)

	go func() {
		// Write a half-written header block, pause, then finish it.
		pw.Write(appendFrame(nil, FrameHeaders, 0, 1, block[:split]))
		time.Sleep(50 * time.Millisecond)
		pw.Write(appendFrame(nil, FrameContinuation, FlagEndHeaders, 1, block[split:]))
	}()

	done := make(chan *Message, 1)
	go func() {
		msg, err := rc.Next()
		if err != nil {
			t.Errorf("next: %v", err)
		}
		done <- msg
	}()

	select {
	case msg := <-done:
		if !reflect.DeepEqual(msg.Fields, fields) {
			t.Fatalf("fields %v, want %v", msg.Fields, fields)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("receiver did not reassemble the half-written block")
	}
}

func TestReceiverRejectsContinuationOnWrongStream(t *testing.T) {
	enc := hpack.NewEncoder(4096)
	block := enc.Encode(nil, []HeaderField{hf("x-a", "1")})

	var stream []byte
	stream = appendFrame(stream, FrameHeaders, 0, 1, block[:1])
	stream = appendFrame(stream, FrameContinuation, FlagEndHeaders, 3, block[1:])

	rc := NewReceiver(bytes.NewReader(stream), 4096)
	if _, err := rc.Next(); err == nil {
		t.Fatal("expected protocol error for CONTINUATION on another stream")
	}
}
