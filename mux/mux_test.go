package mux

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func parseAll(t *testing.T, raw []byte) []*Frame {
	t.Helper()
	p := NewParser(1 << 20)
	p.Feed(raw)
	var out []*Frame
	for {
		f, err := p.Next()
		if err == ErrNeedMore {
			return out
		}
		if err != nil {
			t.Fatalf("parse collected output: %v", err)
		}
		out = append(out, f)
	}
}

// --- Parser: arbitrary chunking, need-more vs bad data ---------------------

func TestParserArbitraryChunking(t *testing.T) {
	frames := []*Frame{
		{Type: FrameData, StreamID: 1, Payload: []byte("hello")},
		{Type: FrameWindowUpdate, StreamID: 1, Payload: WindowUpdatePayload(42)},
		{Type: FrameData, StreamID: 3, Flags: FlagEndStream},
		{Type: FrameRstStream, StreamID: 1, Payload: rstPayload(ErrCodeStreamClosed)},
	}
	var raw []byte
	for _, f := range frames {
		raw = append(raw, encodeFrame(f)...)
	}
	// Feed byte by byte: every strict prefix must report ErrNeedMore, never
	// a spurious frame or error.
	p := NewParser(1 << 14)
	next := 0
	for i, b := range raw {
		p.Feed([]byte{b})
		for {
			f, err := p.Next()
			if err == ErrNeedMore {
				break
			}
			if err != nil {
				t.Fatalf("byte %d: unexpected error %v", i, err)
			}
			want := frames[next]
			next++
			if f.Type != want.Type || f.StreamID != want.StreamID ||
				f.Flags != want.Flags || !bytes.Equal(f.Payload, want.Payload) {
				t.Fatalf("frame %d mismatch: got %+v want %+v", next-1, f, want)
			}
		}
	}
	if next != len(frames) {
		t.Fatalf("decoded %d frames, want %d", next, len(frames))
	}
	if p.Buffered() != 0 {
		t.Fatalf("leftover bytes: %d", p.Buffered())
	}
}

func TestParserLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	p := NewParser(16)
	// Header alone declares a 256-byte payload. The parser must reject it
	// immediately, without waiting for (or allocating) the payload.
	hdr := []byte{0x00, 0x01, 0x00, byte(FrameData), 0, 0, 0, 0, 1}
	p.Feed(hdr)
	_, err := p.Next()
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != ErrCodeFrameSize {
		t.Fatalf("want FRAME_SIZE_ERROR, got %v", err)
	}
}

func TestParserBadData(t *testing.T) {
	cases := map[string][]byte{
		"unknown type":        {0, 0, 0, 0x7f, 0, 0, 0, 0, 1},
		"DATA on stream 0":    {0, 0, 1, byte(FrameData), 0, 0, 0, 0, 0, 'x'},
		"short WINDOW_UPDATE": {0, 0, 4, byte(FrameWindowUpdate), 0, 0, 0, 0, 1, 1, 2, 3, 4},
		"RST on stream 0":     {0, 0, 4, byte(FrameRstStream), 0, 0, 0, 0, 0, 0, 0, 0, 1},
		"short RST":           {0, 0, 2, byte(FrameRstStream), 0, 0, 0, 0, 1, 0, 1},
	}
	for name, raw := range cases {
		p := NewParser(1 << 14)
		p.Feed(raw)
		_, err := p.Next()
		var pe *ProtocolError
		if !errors.As(err, &pe) {
			t.Fatalf("%s: want ProtocolError, got %v", name, err)
		}
	}
}

// --- Flow control ----------------------------------------------------------

func TestDuplicateWindowUpdateNotDoubleCounted(t *testing.T) {
	c := newConn(Config{InitialWindow: 10})
	s, err := c.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	apply := func(wm uint64) {
		c.handleWindowUpdateLocked(&Frame{Type: FrameWindowUpdate, StreamID: s.id, Payload: WindowUpdatePayload(wm)})
	}
	apply(5)
	got1 := s.sendWindow
	apply(5) // exact replay
	apply(3) // stale, older watermark
	got2 := s.sendWindow
	apply(8) // genuine advance
	got3 := s.sendWindow
	c.mu.Unlock()
	if got1 != 15 || got2 != 15 || got3 != 18 {
		t.Fatalf("sendWindow = %d,%d,%d; want 15,15,18", got1, got2, got3)
	}
}

func TestWindowExhaustionDoesNotBlockOtherStream(t *testing.T) {
	c := newConn(Config{InitialWindow: 4, ConnWindow: 64, WriteBudget: 1 << 16})
	a, _ := c.OpenStream()
	b, _ := c.OpenStream()
	a.Write([]byte("aaaaaaaa")) // 8 bytes queued, only 4 fit A's window
	b.Write([]byte("bbbb"))

	c.mu.Lock()
	out1 := c.collectLocked()
	c.mu.Unlock()
	got := parseAll(t, out1)
	var aBytes, bBytes int
	for _, f := range got {
		switch f.StreamID {
		case a.ID():
			aBytes += len(f.Payload)
		case b.ID():
			bBytes += len(f.Payload)
		}
	}
	if aBytes != 4 || bBytes != 4 {
		t.Fatalf("first flush: A=%d B=%d bytes, want 4 and 4", aBytes, bBytes)
	}

	// Both windows are now exhausted; nothing may move.
	a.Write([]byte("more"))
	b.Write([]byte("more"))
	c.mu.Lock()
	if out := c.collectLocked(); len(out) != 0 {
		t.Fatalf("expected no output with exhausted windows, got %d bytes", len(out))
	}
	// Refill only B. A stays exhausted and must not block B.
	c.handleWindowUpdateLocked(&Frame{Type: FrameWindowUpdate, StreamID: b.ID(), Payload: WindowUpdatePayload(8)})
	out2 := c.collectLocked()
	c.mu.Unlock()
	for _, f := range parseAll(t, out2) {
		if f.StreamID != b.ID() {
			t.Fatalf("stream A emitted data while its window was exhausted")
		}
	}
	if n := len(parseAll(t, out2)); n == 0 {
		t.Fatal("stream B made no progress despite available window")
	}
}

func TestControlFramesPrioritizedWithinBudget(t *testing.T) {
	c := newConn(Config{InitialWindow: 64, ConnWindow: 64, WriteBudget: 22})
	s, _ := c.OpenStream()
	s.Write(bytes.Repeat([]byte("d"), 32))

	rst := &Frame{Type: FrameRstStream, StreamID: 99, Payload: rstPayload(ErrCodeNo)} // 13 bytes
	c.mu.Lock()
	c.queueCtrlLocked(rst)
	out1 := c.collectLocked() // budget 22: control (13) fits, 9 left < header+1 data
	out2 := c.collectLocked()
	c.mu.Unlock()

	f1 := parseAll(t, out1)
	if len(f1) != 1 || f1[0].Type != FrameRstStream {
		t.Fatalf("first cycle must emit only the control frame, got %v", f1)
	}
	f2 := parseAll(t, out2)
	if len(f2) != 1 || f2[0].Type != FrameData || len(f2[0].Payload) != 13 {
		t.Fatalf("second cycle must emit budget-limited data, got %v", f2)
	}
}

// --- Close semantics -------------------------------------------------------

func TestDataAfterCloseRejected(t *testing.T) {
	var lateID uint32
	var lateN int
	var lateCalled bool
	c := newConn(Config{
		InitialWindow: 64,
		OnLateData:    func(id uint32, n int) { lateCalled, lateID, lateN = true, id, n },
	})
	var cb callbacks

	c.mu.Lock()
	// Peer opens stream 2 and half-closes with END_STREAM.
	if err := c.handleDataLocked(&Frame{Type: FrameData, StreamID: 2, Flags: FlagEndStream, Payload: []byte("hi")}, &cb); err != nil {
		t.Fatal(err)
	}
	// Data after remote half-close: stream error, RST_STREAM(STREAM_CLOSED).
	var cb2 callbacks
	if err := c.handleDataLocked(&Frame{Type: FrameData, StreamID: 2, Payload: []byte("x")}, &cb2); err != nil {
		t.Fatalf("stream error must not kill the connection: %v", err)
	}
	// Data after full close: late data, rejected again.
	var cb3 callbacks
	c.handleDataLocked(&Frame{Type: FrameData, StreamID: 2, Payload: []byte("yz")}, &cb3)
	ctrl := c.ctrl
	c.mu.Unlock()

	if cb2.errStream == nil || cb2.err.Code != ErrCodeStreamClosed {
		t.Fatalf("want STREAM_CLOSED stream error, got %+v", cb2.err)
	}
	if !cb3.late || cb3.lateID != 2 || cb3.lateN != 2 {
		t.Fatalf("late data not reported: %+v", cb3)
	}
	if len(ctrl) != 2 {
		t.Fatalf("want 2 RST_STREAM frames queued, got %d", len(ctrl))
	}
	for _, raw := range ctrl {
		fs := parseAll(t, raw)
		if fs[0].Type != FrameRstStream || fs[0].StreamID != 2 ||
			ErrCode(binary32(fs[0].Payload)) != ErrCodeStreamClosed {
			t.Fatalf("want RST_STREAM(STREAM_CLOSED) on stream 2, got %+v", fs[0])
		}
	}
	// Fire the callback path the way handleFrame does.
	if cb3.late && c.cfg.OnLateData != nil {
		c.cfg.OnLateData(cb3.lateID, cb3.lateN)
	}
	if !lateCalled || lateID != 2 || lateN != 2 {
		t.Fatalf("OnLateData not invoked correctly: %v %d %d", lateCalled, lateID, lateN)
	}
}

func binary32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func TestWriteAfterCloseWriteFails(t *testing.T) {
	c := newConn(Config{})
	s, _ := c.OpenStream()
	s.Write([]byte("x"))
	if err := s.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("y")); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("want ErrStreamClosed, got %v", err)
	}
	if err := s.CloseWrite(); err != nil {
		t.Fatalf("double CloseWrite must be a no-op, got %v", err)
	}
}

// --- Integration over net.Pipe ---------------------------------------------

func echoConfig(server bool, ch chan<- *Stream) Config {
	return Config{
		Server:          server,
		InitialWindow:   100,
		ConnWindow:      256,
		MaxFramePayload: 16,
		WriteBudget:     96,
		OnNewStream: func(s *Stream) {
			if ch != nil {
				ch <- s
			}
			go func() {
				io.Copy(s, s) // echo until remote half-close
				s.CloseWrite()
			}()
		},
	}
}

func TestEchoHalfCloseAndFlowControl(t *testing.T) {
	c1, c2 := net.Pipe()
	srv := NewConn(c2, c2, echoConfig(true, nil))
	go srv.Serve()
	defer srv.Close()
	cli := NewConn(c1, c1, echoConfig(false, nil))
	go cli.Serve()
	defer cli.Close()

	s, err := cli.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	msg := bytes.Repeat([]byte("abcdefgh"), 4096) // 32 KiB >> windows: forces WINDOW_UPDATE flow
	if _, err := s.Write(msg); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo mismatch: got %d bytes, want %d", len(got), len(msg))
	}
	if st := s.State(); st != StateClosed {
		t.Fatalf("state = %v, want closed", st)
	}
}

func TestConcurrentStreamsInterleaved(t *testing.T) {
	c1, c2 := net.Pipe()
	srv := NewConn(c2, c2, echoConfig(true, nil))
	go srv.Serve()
	defer srv.Close()
	cli := NewConn(c1, c1, echoConfig(false, nil))
	go cli.Serve()
	defer cli.Close()

	const nStreams = 4
	errCh := make(chan error, nStreams)
	for i := 0; i < nStreams; i++ {
		go func(i int) {
			s, err := cli.OpenStream()
			if err != nil {
				errCh <- err
				return
			}
			msg := bytes.Repeat([]byte{byte('A' + i)}, 5000)
			if _, err := s.Write(msg); err != nil {
				errCh <- err
				return
			}
			s.CloseWrite()
			got, err := io.ReadAll(s)
			if err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(got, msg) {
				errCh <- errors.New("echo mismatch")
				return
			}
			errCh <- nil
		}(i)
	}
	for i := 0; i < nStreams; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for streams")
		}
	}
}

func TestResetStream(t *testing.T) {
	c1, c2 := net.Pipe()
	srvCfg := echoConfig(true, nil)
	srvCfg.OnNewStream = func(s *Stream) { s.Reset(ErrCodeStreamClosed) }
	srv := NewConn(c2, c2, srvCfg)
	go srv.Serve()
	defer srv.Close()
	cli := NewConn(c1, c1, echoConfig(false, nil))
	go cli.Serve()
	defer cli.Close()

	s, _ := cli.OpenStream()
	s.Write([]byte("ping")) // first DATA opens the stream server-side
	var se *StreamError
	if _, err := s.Read(make([]byte, 1)); !errors.As(err, &se) || se.Code != ErrCodeStreamClosed {
		t.Fatalf("want StreamError STREAM_CLOSED, got %v", err)
	}
	if _, err := s.Write([]byte("x")); err == nil {
		t.Fatal("write on reset stream must fail")
	}
}
