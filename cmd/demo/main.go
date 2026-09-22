// Command demo builds an explicit transition table, compiles it, and runs
// a scripted byte stream through a session, printing every transition.
package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"fsm"
)

const (
	stInit   fsm.State = "INIT"
	stEst    fsm.State = "ESTABLISHED"
	stClosed fsm.State = "CLOSED"
)

func buildMachine() *fsm.Machine {
	m, err := fsm.Compile(stInit,
		[]fsm.State{stInit, stEst, stClosed},
		[]fsm.Row{
			// Handshake.
			{From: stInit, On: fsm.EventHello, To: stEst, Action: func(c *fsm.Context) (interface{}, error) {
				return "WELCOME", nil
			}},
			{From: stInit, On: fsm.EventTimeout, To: stClosed, Action: func(c *fsm.Context) (interface{}, error) {
				return "IDLE-TIMEOUT", nil
			}},
			// Business frames.
			{From: stEst, On: fsm.EventPing, To: stEst, Action: func(c *fsm.Context) (interface{}, error) {
				return "PONG", nil
			}},
			{From: stEst, On: fsm.EventData, To: stEst, Action: func(c *fsm.Context) (interface{}, error) {
				p := string(c.Frame.Payload)
				if p == "fail" {
					return nil, errors.New("action: simulated failure")
				}
				return "ECHO:" + strings.ToUpper(p), nil
			}},
			{From: stEst, On: fsm.EventBye, To: stClosed, Action: func(c *fsm.Context) (interface{}, error) {
				return "BYE", nil
			}},
			// Independent events.
			{From: stEst, On: fsm.EventReenter, To: stEst, Action: func(c *fsm.Context) (interface{}, error) {
				return "ERR: already handshaked", nil
			}},
			{From: stEst, On: fsm.EventUnknown, To: stEst, Action: func(c *fsm.Context) (interface{}, error) {
				return fmt.Sprintf("ERR: unknown type 0x%02x", c.Frame.Type), nil
			}},
			{From: stEst, On: fsm.EventTimeout, To: stClosed, Action: func(c *fsm.Context) (interface{}, error) {
				return "IDLE-TIMEOUT", nil
			}},
		})
	if err != nil {
		panic(err)
	}
	return m
}

func report(label string, s *fsm.Session, r *fsm.Result, err error) {
	switch {
	case err == fsm.ErrNeedMoreData:
		fmt.Printf("%-28s -> need more data (buffered=%d)\n", label, s.Buffered())
	case err != nil:
		fmt.Printf("%-28s -> BAD FRAME: %v\n", label, err)
	case r.Deduplicated:
		fmt.Printf("%-28s -> DEDUP replay, cached output %v (state stays %s)\n", label, r.Output, s.State())
	case r.Err != nil:
		fmt.Printf("%-28s -> REJECTED: %v (state stays %s)\n", label, r.Err, s.State())
	default:
		fmt.Printf("%-28s -> output %v, state=%s\n", label, r.Output, s.State())
	}
}

func main() {
	m := buildMachine()
	s := fsm.NewSession(m, 64, 50*time.Millisecond)
	now := time.Now()
	s.SetClock(func() time.Time { return now })

	fmt.Println("== 1. business frame before handshake is rejected ==")
	r := s.Handle(&fsm.Frame{Type: fsm.TypeData, Seq: 1, Payload: []byte("hi")})
	report("DATA seq=1 (pre-handshake)", s, &r, nil)

	fmt.Println("== 2. chunked byte input: HELLO+PING fed in 1-byte pieces ==")
	stream := append(fsm.Encode(fsm.Frame{Type: fsm.TypeHello}),
		fsm.Encode(fsm.Frame{Type: fsm.TypePing})...)
	for i, b := range stream {
		s.Feed([]byte{b})
		res, err := s.Next()
		if err == fsm.ErrNeedMoreData {
			continue
		}
		report(fmt.Sprintf("byte %d completes frame", i), s, res, err)
	}

	fmt.Println("== 3. DATA applies, replay is deduplicated ==")
	r = s.Handle(&fsm.Frame{Type: fsm.TypeData, Seq: 1, Payload: []byte("hello")})
	report("DATA seq=1 \"hello\"", s, &r, nil)
	r = s.Handle(&fsm.Frame{Type: fsm.TypeData, Seq: 1, Payload: []byte("hello")})
	report("DATA seq=1 replay", s, &r, nil)
	r = s.Handle(&fsm.Frame{Type: fsm.TypeData, Seq: 2, Payload: []byte("world")})
	report("DATA seq=2 \"world\"", s, &r, nil)

	fmt.Println("== 4. action failure keeps old state ==")
	r = s.Handle(&fsm.Frame{Type: fsm.TypeData, Seq: 3, Payload: []byte("fail")})
	report("DATA seq=3 \"fail\"", s, &r, nil)

	fmt.Println("== 5. unknown type, re-entry, timeout are independent events ==")
	r = s.Handle(&fsm.Frame{Type: 0x7f})
	report("type 0x7f", s, &r, nil)
	r = s.Handle(&fsm.Frame{Type: fsm.TypeHello})
	report("HELLO again", s, &r, nil)
	now = now.Add(time.Second)
	tr := s.Tick()
	report("Tick after idle", s, &tr, nil)

	fmt.Println("== 6. length quota checked before allocation ==")
	bad := []byte{0xff, 0xff} // declares 65535 bytes, quota is 64
	s2 := fsm.NewSession(m, 64, 0)
	s2.Feed(bad)
	_, err := s2.Next()
	fmt.Printf("%-28s -> %v (need-more? %v)\n", "len=0xffff, quota=64", err, err == fsm.ErrNeedMoreData)
}
