package proxy

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func hf(name, value string) HeaderField {
	return HeaderField{Name: name, Value: value}
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// parseFrames splits a byte stream into frames.
func parseFrames(t *testing.T, b []byte) []FrameHeader {
	t.Helper()
	r := bytes.NewReader(b)
	var hs []FrameHeader
	for r.Len() > 0 {
		h, _, err := readFrame(r, 1<<20)
		if err != nil {
			t.Fatalf("parse frame: %v", err)
		}
		hs = append(hs, h)
	}
	return hs
}

// headerBlockPayloads returns the concatenated header block for the n-th
// HEADERS frame in b.
func headerBlockPayload(t *testing.T, b []byte, n int) []byte {
	t.Helper()
	r := bytes.NewReader(b)
	seen := -1
	for r.Len() > 0 {
		h, p, err := readFrame(r, 1<<20)
		if err != nil {
			t.Fatalf("parse frame: %v", err)
		}
		if h.Type != FrameHeaders {
			continue
		}
		seen++
		if seen == n {
			return p
		}
	}
	t.Fatalf("only %d HEADERS frames found", seen+1)
	return nil
}

func TestSubmitQueuesInOrder(t *testing.T) {
	var buf bytes.Buffer
	s := NewSession(&buf, 4096)
	defer s.Close()

	for i := 0; i < 5; i++ {
		req := Request{
			StreamID: uint32(2*i + 1),
			Tenant:   "alpha",
			Fields: []HeaderField{
				hf(":method", "GET"),
				hf("x-seq", strings.Repeat("a", i+1)),
			},
			EndStream: true,
		}
		if err := s.Submit(req); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	rc := NewReceiver(bytes.NewReader(buf.Bytes()), 4096)
	for i := 0; i < 5; i++ {
		msg, err := rc.Next()
		if err != nil {
			t.Fatalf("next %d: %v", i, err)
		}
		if msg.StreamID != uint32(2*i+1) || !msg.EndStream {
			t.Fatalf("msg %d: stream %d endstream %v", i, msg.StreamID, msg.EndStream)
		}
		want := []HeaderField{hf(":method", "GET"), hf("x-seq", strings.Repeat("a", i+1))}
		if !reflect.DeepEqual(msg.Fields, want) {
			t.Fatalf("msg %d fields %v, want %v", i, msg.Fields, want)
		}
	}
}

func TestTenantSwitchClearsTable(t *testing.T) {
	var buf bytes.Buffer
	s := NewSession(&buf, 4096)
	defer s.Close()

	fields := []HeaderField{hf(":method", "GET"), hf("x-tenant-data", "secret-value")}
	if err := s.Submit(Request{StreamID: 1, Tenant: "alpha", Fields: fields}); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(Request{StreamID: 3, Tenant: "beta", Fields: fields}); err != nil {
		t.Fatal(err)
	}
	// Same tenant again: must compress against the restored table.
	if err := s.Submit(Request{StreamID: 5, Tenant: "beta", Fields: fields}); err != nil {
		t.Fatal(err)
	}

	raw := buf.Bytes()
	block2 := headerBlockPayload(t, raw, 1)
	// Beta's first block must open with clear-table updates: 0 then 4096.
	if block2[0]&0xe0 != 0x20 || block2[0]&0x1f != 0 {
		t.Fatalf("expected leading zero table size update, got %#x", block2[0])
	}
	// The restore update (4096) follows: 0x3f 0xe1 0x1f.
	if !bytes.HasPrefix(block2[1:], []byte{0x3f, 0xe1, 0x1f}) {
		t.Fatalf("missing restore update after clear: % x", block2[:8])
	}

	// Everything must still decode correctly with an independent receiver.
	rc := NewReceiver(bytes.NewReader(raw), 4096)
	for i, stream := range []uint32{1, 3, 5} {
		msg, err := rc.Next()
		if err != nil {
			t.Fatalf("next %d: %v", i, err)
		}
		if msg.StreamID != stream || !reflect.DeepEqual(msg.Fields, fields) {
			t.Fatalf("msg %d: stream %d fields %v", i, msg.StreamID, msg.Fields)
		}
	}

	// Beta's second block must be smaller than its first (uses own entries).
	block3 := headerBlockPayload(t, raw, 2)
	if len(block3) >= len(block2) {
		t.Fatalf("beta's second block not compressed: %d >= %d", len(block3), len(block2))
	}
}

func TestSettingsReductionAndAck(t *testing.T) {
	var buf bytes.Buffer
	s := NewSession(&buf, 4096)
	defer s.Close()

	if err := s.Submit(Request{StreamID: 1, Tenant: "alpha", Fields: []HeaderField{hf("x-a", "1")}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPeerSettings(Settings{HeaderTableSize: 128}); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(Request{StreamID: 3, Tenant: "alpha", Fields: []HeaderField{hf("x-b", "2")}}); err != nil {
		t.Fatal(err)
	}

	raw := buf.Bytes()
	frames := parseFrames(t, raw)
	if frames[1].Type != FrameSettings || frames[1].Flags&FlagAck == 0 || frames[1].StreamID != 0 {
		t.Fatalf("frame 1 is not a SETTINGS ACK: %+v", frames[1])
	}
	// The next header block must open with a table size update <= 128.
	block := headerBlockPayload(t, raw, 1)
	if block[0]&0xe0 != 0x20 {
		t.Fatalf("block after reduction lacks table size update: %#x", block[0])
	}

	rc := NewReceiver(bytes.NewReader(raw), 4096)
	var acked bool
	rc.OnSettingsAck = func() { acked = true }
	for i := 0; i < 2; i++ {
		if _, err := rc.Next(); err != nil {
			t.Fatalf("next %d: %v", i, err)
		}
	}
	if !acked {
		t.Fatal("receiver never saw the SETTINGS ACK")
	}
}

// gateWriter blocks its first Write until released, simulating a connection
// that stalls halfway through a fragmented header block.
type gateWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGateWriter() *gateWriter {
	return &gateWriter{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *gateWriter) Write(p []byte) (int, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buf.Write(p)
}

func TestControlFrameNotInterleavedWithFragmentedBlock(t *testing.T) {
	g := newGateWriter()
	s := NewSession(g, 4096)
	defer s.Close()
	// Force the header block into many CONTINUATION frames.
	s.SetMaxFrameSize(32)

	big := Request{
		StreamID: 1,
		Tenant:   "alpha",
		Fields: []HeaderField{
			hf(":method", "GET"),
			hf("x-pad", strings.Repeat("p", 512)),
		},
	}
	submitDone := make(chan error, 1)
	go func() { submitDone <- s.Submit(big) }()

	// The session is now stalled halfway through writing the header block.
	waitClosed(t, g.entered, "header block write to start")

	// A peer SETTINGS arrives concurrently; its ACK must not interleave.
	settingsDone := make(chan error, 1)
	go func() { settingsDone <- s.ApplyPeerSettings(Settings{HeaderTableSize: 256}) }()

	close(g.release)
	if err := <-submitDone; err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := <-settingsDone; err != nil {
		t.Fatalf("settings: %v", err)
	}

	g.mu.Lock()
	raw := append([]byte(nil), g.buf.Bytes()...)
	g.mu.Unlock()

	frames := parseFrames(t, raw)
	if len(frames) < 3 {
		t.Fatalf("expected fragmented block plus ACK, got %d frames", len(frames))
	}
	// All frames but the last belong to the header block; the ACK is last.
	for i, h := range frames[:len(frames)-1] {
		want := FrameContinuation
		if i == 0 {
			want = FrameHeaders
		}
		if h.Type != want || h.StreamID != 1 {
			t.Fatalf("frame %d: type %#x stream %d, want type %#x stream 1", i, h.Type, h.StreamID, want)
		}
		if i < len(frames)-2 && h.Flags&FlagEndHeaders != 0 {
			t.Fatalf("frame %d ended the block early", i)
		}
	}
	last := frames[len(frames)-1]
	if last.Type != FrameSettings || last.Flags&FlagAck == 0 {
		t.Fatalf("last frame is not the SETTINGS ACK: %+v", last)
	}

	// An independent receiver must decode the stream without observing any
	// interleaving.
	rc := NewReceiver(bytes.NewReader(raw), 4096)
	msg, err := rc.Next()
	if err != nil {
		t.Fatalf("receiver: %v", err)
	}
	if !reflect.DeepEqual(msg.Fields, big.Fields) {
		t.Fatalf("fields %v, want %v", msg.Fields, big.Fields)
	}
}

type failWriter struct {
	writes int
	closed bool
}

func (f *failWriter) Write(p []byte) (int, error) {
	f.writes++
	return 0, errors.New("connection reset")
}

func (f *failWriter) Close() error {
	f.closed = true
	return nil
}

func TestWriteFailureDiscardsConnection(t *testing.T) {
	fw := &failWriter{}
	s := NewSession(fw, 4096)

	err := s.Submit(Request{StreamID: 1, Tenant: "alpha", Fields: []HeaderField{hf("x-a", "1")}})
	if err == nil || err == ErrSessionBroken {
		t.Fatalf("first submit err = %v, want the write error", err)
	}
	if !fw.closed {
		t.Fatal("connection was not closed after write failure")
	}
	if err := s.Submit(Request{StreamID: 3, Tenant: "alpha"}); err != ErrSessionBroken {
		t.Fatalf("submit after failure err = %v, want ErrSessionBroken", err)
	}
	if err := s.ApplyPeerSettings(Settings{HeaderTableSize: 128}); err != ErrSessionBroken {
		t.Fatalf("settings after failure err = %v, want ErrSessionBroken", err)
	}
	if fw.writes != 1 {
		t.Fatalf("writer saw %d writes, want exactly 1", fw.writes)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
