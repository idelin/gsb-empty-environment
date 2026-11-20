package reservation_test

import (
	"bytes"
	"encoding/hex"
	"example.org/noncebook/reservation"
	"testing"
)

func TestArchivedReservation(t *testing.T) {
	b, _ := hex.DecodeString("4e420100010203040506070804030201feffffff0002")
	r, e := reservation.ParseLegacy(b)
	if e != nil || r.Epoch != 0x01020304 || r.Start != 0xfffffffe || r.Count != 2 {
		t.Fatalf("decoded %v, %v", r, e)
	}
	out, e := r.MarshalBinary()
	if e != nil || !bytes.Equal(out, b) {
		t.Fatal("archived bytes changed")
	}
	b[21] = 3
	if _, e := reservation.ParseLegacy(b); e == nil {
		t.Fatal("wrapped interval accepted")
	}
}
