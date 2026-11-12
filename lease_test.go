package netsec

import (
	"sync"
	"testing"
	"time"
)

func TestCloseDrainsBeforeReuse(t *testing.T) {
	p := NewPool(1)
	a, err := p.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	seqs := make([]uint64, 3)
	for i := range seqs {
		seqs[i], err = a.Submit()
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	closed := make(chan struct{})
	go func() {
		a.Close()
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("Close returned before pending submissions drained")
	case <-time.After(20 * time.Millisecond):
	}

	// Driver confirmations arrive out of order.
	for _, seq := range []uint64{seqs[2], seqs[0], seqs[1]} {
		if err := a.Confirm(seq); err != nil {
			t.Fatalf("confirm %d: %v", seq, err)
		}
	}

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after drain")
	}

	// Vector must be reusable only after the drain completed.
	b, err := p.Acquire()
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if b.Vector() != a.Vector() {
		t.Fatalf("expected vector reuse, got %v want %v", b.Vector(), a.Vector())
	}
	if b.Generation() <= a.Generation() {
		t.Fatalf("generation did not advance: %d -> %d", a.Generation(), b.Generation())
	}
}

func TestSubmitForbiddenWhileClosing(t *testing.T) {
	p := NewPool(1)
	a, _ := p.Acquire()
	if _, err := a.Submit(); err != nil {
		t.Fatalf("submit: %v", err)
	}

	closing := make(chan struct{})
	go func() {
		a.Close()
		close(closing)
	}()
	time.Sleep(20 * time.Millisecond)

	if _, err := a.Submit(); err != ErrLeaseClosed {
		t.Fatalf("submit during close: got %v, want ErrLeaseClosed", err)
	}

	if err := a.Confirm(1); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	<-closing
}

func TestLateInterruptDroppedWithEvidence(t *testing.T) {
	p := NewPool(1)
	a, _ := p.Acquire()
	oldGen := a.Generation()
	vec := a.Vector()
	a.Close()

	var delivered []Event
	var mu sync.Mutex
	b, err := p.Acquire()
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	b.SetHandler(func(ev Event) {
		mu.Lock()
		delivered = append(delivered, ev)
		mu.Unlock()
	})

	if p.Interrupt(Event{Vector: vec, Generation: oldGen, Payload: 0xdead}) {
		t.Fatal("late interrupt was delivered")
	}
	if !p.Interrupt(Event{Vector: vec, Generation: b.Generation(), Payload: 0xbeef}) {
		t.Fatal("current-generation interrupt was dropped")
	}

	mu.Lock()
	if len(delivered) != 1 || delivered[0].Payload != 0xbeef {
		t.Fatalf("delivered events = %+v, want only payload 0xbeef", delivered)
	}
	mu.Unlock()

	drops := p.Drops()
	if len(drops) != 1 {
		t.Fatalf("drops = %+v, want exactly one record", drops)
	}
	d := drops[0]
	if d.Vector != vec || d.Generation != oldGen || d.Payload != 0xdead {
		t.Fatalf("drop evidence = %+v, want vector %v gen %d payload 0xdead", d, vec, oldGen)
	}
}

func TestUnknownGenerationOnIdleVector(t *testing.T) {
	p := NewPool(1)
	if p.Interrupt(Event{Vector: 0, Generation: 42, Payload: 1}) {
		t.Fatal("interrupt on idle vector was delivered")
	}
	drops := p.Drops()
	if len(drops) != 1 || drops[0].Reason != "vector not leased" {
		t.Fatalf("drops = %+v, want one 'vector not leased' record", drops)
	}
}

func TestPoolExhaustion(t *testing.T) {
	p := NewPool(1)
	if _, err := p.Acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := p.Acquire(); err != ErrPoolExhausted {
		t.Fatalf("second acquire: got %v, want ErrPoolExhausted", err)
	}
}

func TestBadConfirm(t *testing.T) {
	p := NewPool(1)
	a, _ := p.Acquire()
	if err := a.Confirm(99); err != ErrBadConfirm {
		t.Fatalf("confirm unknown seq: got %v, want ErrBadConfirm", err)
	}
	a.Close()
}
