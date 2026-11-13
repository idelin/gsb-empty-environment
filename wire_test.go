package netsec

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestWireMessagesRoundTrip(t *testing.T) {
	messages := []Message{
		&Hello{SessionID: []byte("wire-session"), Initiator: "alice", X: bytes.Repeat([]byte{0x02}, 33)},
		&Response{SessionID: []byte("wire-session"), Responder: "bob", Y: bytes.Repeat([]byte{0x03}, 33), Signature: bytes.Repeat([]byte{0xaa}, 64)},
		&InitiatorAuth{SessionID: []byte("wire-session"), Initiator: "alice", Signature: bytes.Repeat([]byte{0xbb}, 64)},
		&Confirmation{SessionID: []byte("wire-session"), Role: RoleA, MAC: bytes.Repeat([]byte{0xcc}, 32)},
	}
	for index, message := range messages {
		encoded, err := EncodeMessage(message)
		if err != nil {
			t.Fatalf("message %d encode: %v", index, err)
		}
		decoded, err := DecodeMessage(encoded)
		if err != nil {
			t.Fatalf("message %d decode: %v", index, err)
		}
		if !reflect.DeepEqual(message, decoded) {
			t.Fatalf("message %d mismatch:\n got %#v\nwant %#v", index, decoded, message)
		}
	}
}

func TestWireDecoderRejectsMalformedMessages(t *testing.T) {
	valid, err := EncodeMessage(&Hello{
		SessionID: []byte("wire"), Initiator: "alice", X: bytes.Repeat([]byte{0x02}, 33),
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := [][]byte{
		nil,
		{0xff},
		valid[:len(valid)-1],
		append(append([]byte(nil), valid...), 0x00),
		[]byte{messageHello, 0xff, 0xff, 0xff, 0xff},
	}
	for index, data := range cases {
		if _, err := DecodeMessage(data); !isMalformed(err) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}

	badRole := &Confirmation{SessionID: []byte("wire"), Role: Role(9), MAC: bytes.Repeat([]byte{0xcc}, 32)}
	if _, err := badRole.MarshalBinary(); !isMalformed(err) {
		t.Fatalf("bad role marshal error = %v", err)
	}
}

func isMalformed(err error) bool {
	return errors.Is(err, ErrMalformedMessage)
}

func TestWireFieldsAreCopied(t *testing.T) {
	sessionID := []byte("copy")
	hello := &Hello{SessionID: sessionID, Initiator: "alice", X: []byte{0x02}}
	encoded, err := hello.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	sessionID[0] = 'X'
	hello.X[0] = 0x03
	var decoded Hello
	if err := decoded.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded.SessionID) != "copy" || decoded.X[0] != 0x02 {
		t.Fatal("wire decoder aliased caller buffers")
	}
}
