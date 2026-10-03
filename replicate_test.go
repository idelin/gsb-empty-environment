package roaring

import (
	"errors"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestReplicationUnderPacketLoss(t *testing.T) {
	a, b := ConnPair(16)
	senderConn := &LossyConn{Inner: a, DropRate: 0.3, Seed: 1}
	receiverConn := &LossyConn{Inner: b, DropRate: 0.3, Seed: 2}

	receiverBm := New()
	var applyMu sync.Mutex
	receiver := &Receiver{Conn: receiverConn, Apply: func(rec []byte) {
		applyMu.Lock()
		defer applyMu.Unlock()
		if err := applyRecord(receiverBm, rec); err != nil {
			t.Errorf("apply: %v", err)
		}
	}}
	done := make(chan error, 1)
	go func() { done <- receiver.Run() }()

	sender := &Sender{Conn: senderConn, Timeout: 2 * time.Millisecond}
	expected := New()
	rnd := rand.New(rand.NewSource(5))
	const records = 300
	for i := 0; i < records; i++ {
		key := uint16(rnd.Intn(8))
		vals := []uint16{uint16(rnd.Intn(65536)), uint16(rnd.Intn(65536))}
		if err := sender.Send(encodeAddRecord(key, vals)); err != nil {
			t.Fatalf("send: %v", err)
		}
		for _, v := range vals {
			expected.Add(uint32(key)<<16 | uint32(v))
		}
	}

	// Stop-and-wait ARQ: once Send returned, the record was applied and
	// acknowledged. Still, poll defensively for convergence.
	deadline := time.Now().Add(5 * time.Second)
	for {
		applyMu.Lock()
		ok := receiverBm.Cardinality() == expected.Cardinality()
		applyMu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("receiver did not converge")
		}
		time.Sleep(5 * time.Millisecond)
	}

	senderConn.Close()
	receiverConn.Close()
	if err := <-done; !errors.Is(err, errConnClosed) {
		t.Fatalf("receiver exit = %v, want errConnClosed", err)
	}

	applyMu.Lock()
	eq := receiverBm.Equals(expected)
	applyMu.Unlock()
	if !eq {
		t.Fatal("replicated bitmap mismatch")
	}
	if senderConn.Dropped == 0 {
		t.Fatal("fault injection dropped no packets; test is vacuous")
	}
	t.Logf("sender: dropped=%d delivered=%d (retransmissions included)",
		senderConn.Dropped, senderConn.Delivered)
}

func TestReplicationCorruptPacketsIgnored(t *testing.T) {
	a, b := ConnPair(4)
	receiver := &Receiver{Conn: b, Apply: func(rec []byte) {}}
	done := make(chan error, 1)
	go func() { done <- receiver.Run() }()

	// Garbage and truncated packets must not crash or wedge the receiver.
	a.Send([]byte{0xde, 0xad})
	a.Send([]byte{})
	pkt := encodePacket(pktData, 1, encodeAddRecord(1, []uint16{7}))
	corrupt := append([]byte(nil), pkt...)
	corrupt[len(corrupt)-1] ^= 0xff
	a.Send(corrupt)
	a.Send(pkt) // valid copy still gets through

	sender := &Sender{Conn: a, Timeout: 5 * time.Millisecond}
	if err := sender.Send(encodeAddRecord(2, []uint16{9})); err != nil {
		t.Fatalf("send after corrupt packets: %v", err)
	}
	a.Close()
	b.Close()
	if err := <-done; !errors.Is(err, errConnClosed) {
		t.Fatalf("receiver exit = %v", err)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		a, b := ConnPair(4)
		receiver := &Receiver{Conn: b}
		done := make(chan error, 1)
		go func() { done <- receiver.Run() }()
		sender := &Sender{Conn: a, Timeout: time.Millisecond}
		if err := sender.Send(encodeAddRecord(1, []uint16{1})); err != nil {
			t.Fatal(err)
		}
		a.Close()
		b.Close()
		<-done
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
