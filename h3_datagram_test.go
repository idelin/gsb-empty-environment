package netsec_test

import (
	"errors"
	"reflect"
	"testing"

	"example.org/netsec/q0508"
)

type received struct {
	streamID uint64
	payload  string
}

func newTestDispatcher(t *testing.T, local, peer bool) (*netsec.Dispatcher, *[]received) {
	t.Helper()
	var got []received
	d := netsec.NewDispatcher(
		netsec.WithSettings(local, peer),
		netsec.WithHandler(func(streamID uint64, payload []byte) {
			got = append(got, received{streamID: streamID, payload: string(payload)})
		}),
	)
	return d, &got
}

func datagram(t *testing.T, quarterStreamID uint64, payload string) []byte {
	t.Helper()
	wire, err := netsec.EncodeQuarterStreamID(quarterStreamID, []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestUnnegotiatedDatagramClosesConnection(t *testing.T) {
	for name, settings := range map[string][2]bool{
		"neither":    {false, false},
		"local only": {true, false},
		"peer only":  {false, true},
	} {
		t.Run(name, func(t *testing.T) {
			d, got := newTestDispatcher(t, settings[0], settings[1])
			d.OpenConnectStream(0)
			err := d.HandleDatagram(datagram(t, 0, "payload"))
			if !errors.Is(err, netsec.ErrH3DatagramError) {
				t.Fatalf("got %v, want H3_DATAGRAM_ERROR", err)
			}
			var h3err *netsec.H3ConnectionError
			if !errors.As(err, &h3err) || h3err.Code != netsec.H3DatagramErrorCode {
				t.Fatalf("error does not carry H3_DATAGRAM_ERROR code: %#v", err)
			}
			if len(*got) != 0 {
				t.Fatalf("handler called before negotiation: %#v", *got)
			}
		})
	}
}

func TestUnnegotiatedCheckPrecedesStreamFiltering(t *testing.T) {
	d, got := newTestDispatcher(t, false, false)
	if err := d.HandleStreamDatagram(1, []byte("foreign")); !errors.Is(err, netsec.ErrH3DatagramError) {
		t.Fatalf("got %v, want H3_DATAGRAM_ERROR", err)
	}
	if len(*got) != 0 {
		t.Fatalf("handler called: %#v", *got)
	}
}

func TestForeignStreamIDsAreSilentlyDropped(t *testing.T) {
	for _, streamID := range []uint64{1, 2, 3, 5, 6, 7} {
		if netsec.ValidateStreamID(streamID) {
			t.Fatalf("stream %d must not be peer-initiated bidirectional", streamID)
		}
		// Even if a buggy caller marks a foreign stream as CONNECT, the
		// dispatcher must not deliver datagrams for it.
		d, got := newTestDispatcher(t, true, true)
		if d.OpenConnectStream(streamID) {
			t.Fatalf("stream %d was accepted as a datagram channel", streamID)
		}
		err := d.HandleStreamDatagram(streamID, []byte("foreign"))
		if err != nil {
			t.Fatalf("stream %d: got fatal error, want silent drop: %v", streamID, err)
		}
		if len(*got) != 0 {
			t.Fatalf("stream %d was delivered: %#v", streamID, *got)
		}
		if n := d.BufferedDatagrams(streamID); n != 0 {
			t.Fatalf("stream %d buffered %d datagrams", streamID, n)
		}
	}
}

func TestDatagramBeforeStreamCreationIsBuffered(t *testing.T) {
	d, got := newTestDispatcher(t, true, true)
	const streamID = uint64(0)
	if err := d.HandleDatagram(datagram(t, streamID/4, "early")); err != nil {
		t.Fatal(err)
	}
	if n := d.BufferedDatagrams(streamID); n != 1 {
		t.Fatalf("buffered %d datagrams, want 1", n)
	}
	d.OpenConnectStream(streamID)
	if want, have := []received{{streamID, "early"}}, *got; !reflect.DeepEqual(want, have) {
		t.Fatalf("got %#v, want %#v", have, want)
	}
}

func TestBufferCopiesCallerPayload(t *testing.T) {
	d, got := newTestDispatcher(t, true, true)
	const streamID = uint64(4)
	payload := []byte("original")
	if err := d.HandleStreamDatagram(streamID, payload); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X'
	d.OpenConnectStream(streamID)
	if want, have := []received{{streamID, "original"}}, *got; !reflect.DeepEqual(want, have) {
		t.Fatalf("got %#v, want %#v", have, want)
	}
}

func TestClosedStreamDatagramsAreDropped(t *testing.T) {
	d, got := newTestDispatcher(t, true, true)
	const streamID = uint64(8)
	d.OpenConnectStream(streamID)
	if err := d.HandleQuarterStreamID(streamID/4, []byte("before close")); err != nil {
		t.Fatal(err)
	}
	d.CloseStream(streamID)
	if err := d.HandleQuarterStreamID(streamID/4, []byte("after close")); err != nil {
		t.Fatalf("closed stream produced fatal error: %v", err)
	}
	if d.StreamState(streamID) != netsec.StreamClosed {
		t.Fatal("closed state was not retained")
	}
	if n := d.BufferedDatagrams(streamID); n != 0 {
		t.Fatalf("closed stream has %d buffered datagrams", n)
	}
	if want, have := []received{{streamID, "before close"}}, *got; !reflect.DeepEqual(want, have) {
		t.Fatalf("got %#v, want %#v", have, want)
	}
}

func TestBufferKeepsNewestSixteenAndDropsOldest(t *testing.T) {
	d, got := newTestDispatcher(t, true, true)
	const streamID = uint64(12)
	const quarterStreamID = streamID / 4

	for i := 0; i < 20; i++ {
		payload := make([]byte, 1)
		payload[0] = byte('A' + i)
		if err := d.HandleQuarterStreamID(quarterStreamID, payload); err != nil {
			t.Fatalf("datagram %d: %v", i, err)
		}
	}
	if n := d.BufferedDatagrams(streamID); n != netsec.MaxBufferedDatagrams {
		t.Fatalf("buffered %d datagrams, want %d", n, netsec.MaxBufferedDatagrams)
	}

	if !d.OpenConnectStream(streamID) {
		t.Fatal("stream 12 should be accepted")
	}
	if len(*got) != netsec.MaxBufferedDatagrams {
		t.Fatalf("delivered %d datagrams, want %d", len(*got), netsec.MaxBufferedDatagrams)
	}
	for i, r := range *got {
		want := string([]byte{byte('A' + 4 + i)})
		if r.payload != want {
			t.Fatalf("delivery %d = %q, want %q", i, r.payload, want)
		}
		if r.streamID != streamID {
			t.Fatalf("delivery %d stream = %d, want %d", i, r.streamID, streamID)
		}
	}
	if n := d.BufferedDatagrams(streamID); n != 0 {
		t.Fatalf("buffer not drained: %d", n)
	}
}

func TestKnownNonConnectStreamDropsDatagrams(t *testing.T) {
	d, got := newTestDispatcher(t, true, true)
	const streamID = uint64(16)
	if !d.RegisterRequest(streamID, "GET") {
		t.Fatal("GET stream should be a valid peer stream")
	}
	if err := d.HandleQuarterStreamID(streamID/4, []byte("GET payload")); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 0 {
		t.Fatalf("GET stream delivered datagrams: %#v", *got)
	}
	if n := d.BufferedDatagrams(streamID); n != 0 {
		t.Fatalf("GET stream buffered %d datagrams", n)
	}
}

func TestQuarterStreamIDOverLimitIsFatal(t *testing.T) {
	d, got := newTestDispatcher(t, true, true)
	d.OpenConnectStream(0)

	payload := []byte("x")
	header, err := netsec.EncodeVarint(1 << 60)
	if err != nil {
		t.Fatal(err)
	}
	wire := append(header, payload...)
	if err := d.HandleDatagram(wire); !errors.Is(err, netsec.ErrH3DatagramError) {
		t.Fatalf("got %v, want H3_DATAGRAM_ERROR", err)
	}
	if len(*got) != 0 {
		t.Fatalf("over-limit datagram was delivered: %#v", *got)
	}
}

func TestQuarterStreamIDBoundaries(t *testing.T) {
	maxStreamID := uint64(netsec.MaxQuarterStreamID) << 2
	d, got := newTestDispatcher(t, true, true)
	if !d.OpenConnectStream(maxStreamID) {
		t.Fatal("largest legal peer stream ID was rejected")
	}
	if err := d.HandleQuarterStreamID(netsec.MaxQuarterStreamID, []byte("max")); err != nil {
		t.Fatalf("largest legal Quarter Stream ID: %v", err)
	}
	if want, have := []received{{maxStreamID, "max"}}, *got; !reflect.DeepEqual(want, have) {
		t.Fatalf("got %#v, want %#v", have, want)
	}
	if err := d.HandleQuarterStreamID(netsec.MaxQuarterStreamID+1, []byte("too large")); !errors.Is(err, netsec.ErrH3DatagramError) {
		t.Fatalf("got %v, want H3_DATAGRAM_ERROR", err)
	}
}

func TestMalformedQuarterStreamIDIsFatal(t *testing.T) {
	d, _ := newTestDispatcher(t, true, true)
	for _, wire := range [][]byte{nil, {0x40}, {0x80, 0x00, 0x00}} {
		if err := d.HandleDatagram(wire); !errors.Is(err, netsec.ErrH3DatagramError) {
			t.Fatalf("wire %x: got %v, want H3_DATAGRAM_ERROR", wire, err)
		}
	}
}

func TestVarintRoundTrip(t *testing.T) {
	for _, value := range []uint64{0, 1, 63, 64, 16383, 16384, (1 << 30) - 1, 1 << 30, (1 << 62) - 1} {
		wire, err := netsec.EncodeVarint(value)
		if err != nil {
			t.Fatalf("%d: %v", value, err)
		}
		decoded, rest, err := netsec.DecodeVarint(append(wire, 0xaa))
		if err != nil {
			t.Fatalf("%d: %v", value, err)
		}
		if decoded != value || len(rest) != 1 || rest[0] != 0xaa {
			t.Fatalf("%d: got (%d, %x, %v)", value, decoded, rest, err)
		}
	}
	if _, _, err := netsec.DecodeVarint([]byte{0xc0}); !errors.Is(err, netsec.ErrInvalidVarint) {
		t.Fatalf("truncated varint: got %v", err)
	}
}

func TestConnectionErrorCallbackReportsCode(t *testing.T) {
	var code uint64
	var reason string
	d := netsec.NewDispatcher(
		netsec.WithSettings(false, false),
		netsec.WithConnectionErrorHandler(func(c uint64, r string) {
			code, reason = c, r
		}),
	)
	_ = d.HandleDatagram(datagram(t, 0, "x"))
	if code != netsec.H3DatagramErrorCode {
		t.Fatalf("code = %#x, want %#x", code, netsec.H3DatagramErrorCode)
	}
	if reason == "" {
		t.Fatal("missing error reason")
	}
}

func TestStreamClosedBeforeCreationDiscardsBuffer(t *testing.T) {
	d, got := newTestDispatcher(t, true, true)
	const streamID = uint64(20)
	_ = d.HandleQuarterStreamID(streamID/4, []byte("pending"))
	if n := d.BufferedDatagrams(streamID); n != 1 {
		t.Fatalf("buffered %d, want 1", n)
	}
	d.CloseStream(streamID)
	if err := d.HandleQuarterStreamID(streamID/4, []byte("late")); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 0 {
		t.Fatalf("closed stream delivered buffered datagram: %#v", *got)
	}
}
