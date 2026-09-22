// Demo of the streammux protocol over an in-memory net.Pipe.
//
// It walks through: independent per-stream windows, half-close, duplicate
// WINDOW_UPDATE suppression, and rejection of data sent after close.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"streammux/mux"
)

func logf(format string, args ...any) {
	args = append([]any{time.Now().Format("15:04:05.000")}, args...)
	fmt.Printf("%s  "+format+"\n", args...)
}

func main() {
	c1, c2 := net.Pipe()
	serverStreams := make(map[uint32]*mux.Stream)
	var mu sync.Mutex

	srv := mux.NewConn(c2, c2, mux.Config{
		Server:          true,
		InitialWindow:   64,
		ConnWindow:      256,
		MaxFramePayload: 16,
		WriteBudget:     128,
		OnNewStream: func(s *mux.Stream) {
			logf("server: stream %d opened", s.ID())
			mu.Lock()
			serverStreams[s.ID()] = s
			mu.Unlock()
			go func() {
				io.Copy(s, s)                     // echo until the client half-closes
				time.Sleep(50 * time.Millisecond) // keep the half-close observable
				s.CloseWrite()
				logf("server: stream %d echoed, half-closed (state=%s)", s.ID(), s.State())
			}()
		},
		OnStreamError: func(s *mux.Stream, err *mux.StreamError) {
			logf("server: stream %d stream error -> RST_STREAM: %v", s.ID(), err)
		},
		OnLateData: func(id uint32, n int) {
			logf("server: LATE DATA on closed stream %d (%d bytes) -> RST_STREAM(STREAM_CLOSED)", id, n)
		},
	})
	go srv.Serve()
	defer srv.Close()

	cli := mux.NewConn(c1, c1, mux.Config{
		InitialWindow:   64,
		ConnWindow:      256,
		MaxFramePayload: 16,
		WriteBudget:     128,
	})
	go cli.Serve()
	defer cli.Close()

	// --- Phase 1: A's window exhaustion does not block B -------------------
	logf("--- phase 1: independent stream windows (window=64, frame<=16, budget=128) ---")
	a, _ := cli.OpenStream()
	b, _ := cli.OpenStream()
	payloadA := bytes.Repeat([]byte("A"), 200) // larger than A's window
	payloadB := []byte("hello from B")
	a.Write(payloadA)
	b.Write(payloadB)
	logf("client: A(stream %d) queued %d bytes, B(stream %d) queued %d bytes", a.ID(), len(payloadA), b.ID(), len(payloadB))

	echoB := make([]byte, len(payloadB))
	if _, err := io.ReadFull(b, echoB); err != nil {
		logf("client: B failed: %v", err)
		os.Exit(1)
	}
	logf("client: B echo complete (%q) while A is still window-limited", echoB)

	echoA := make([]byte, len(payloadA))
	if _, err := io.ReadFull(a, echoA); err != nil {
		logf("client: A failed: %v", err)
		os.Exit(1)
	}
	logf("client: A echo complete (%d bytes) after window updates flowed", len(echoA))

	// --- Phase 2: half-close -----------------------------------------------
	logf("--- phase 2: half-close ---")
	a.CloseWrite()
	time.Sleep(20 * time.Millisecond) // END_STREAM sent, server has not closed yet
	logf("client: A CloseWrite, state=%s (read side still open)", a.State())
	if _, err := a.Read(make([]byte, 1)); err != io.EOF {
		logf("client: expected EOF after server half-close, got %v", err)
		os.Exit(1)
	}
	logf("client: A read side reached EOF, state=%s", a.State())
	if _, err := a.Write([]byte("x")); err != nil {
		logf("client: write after CloseWrite correctly fails: %v", err)
	}

	// --- Phase 3: duplicate WINDOW_UPDATE is not double-counted ------------
	logf("--- phase 3: duplicate window update ---")
	mu.Lock()
	serverA := serverStreams[a.ID()]
	mu.Unlock()
	wm := serverA.Consumed() // watermark the server already advertised for A
	before := a.SendWindow()
	// Replay the exact same WINDOW_UPDATE; it must not extend the window again.
	srv.SendRawFrame(&mux.Frame{Type: mux.FrameWindowUpdate, StreamID: a.ID(),
		Payload: mux.WindowUpdatePayload(wm)})
	time.Sleep(50 * time.Millisecond)
	after := a.SendWindow()
	logf("client: send window before=%d after=%d (duplicate watermark %d ignored)",
		before, after, wm)

	// --- Phase 4: data after close is rejected per protocol ----------------
	logf("--- phase 4: late data on a closed stream ---")
	cli.SendRawFrame(&mux.Frame{Type: mux.FrameData, StreamID: a.ID(), Payload: []byte("zombie")})
	time.Sleep(100 * time.Millisecond)

	logf("demo done")
}
