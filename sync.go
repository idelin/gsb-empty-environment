package fiberdbg

import "fmt"

// Channel 是 Fiber 间的通信原语，语义类似带缓冲的 Go channel。
type Channel struct {
	Name  string
	buf   []interface{}
	cap   int
	sendQ []sendWaiter
	recvQ []*Fiber
}

type sendWaiter struct {
	f *Fiber
	v interface{}
}

// NewChannel 创建一个容量为 capacity 的命名 Channel（0 表示无缓冲）。
func NewChannel(name string, capacity int) *Channel {
	return &Channel{Name: name, cap: capacity}
}

func (s *Scheduler) handleSend(f *Fiber, ch *Channel, v interface{}) {
	if len(ch.recvQ) > 0 {
		r := ch.recvQ[0]
		ch.recvQ = ch.recvQ[1:]
		r.inbox = v
		s.wake(r, fmt.Sprintf("received value from fiber %d(%s) on channel '%s'",
			f.ID, f.Name, ch.Name))
		s.setState(f, StateReady, fmt.Sprintf("send on channel '%s' handed to receiver", ch.Name))
		s.ready = append(s.ready, f)
		return
	}
	if len(ch.buf) < ch.cap {
		ch.buf = append(ch.buf, v)
		s.setState(f, StateReady, fmt.Sprintf("send on channel '%s' buffered", ch.Name))
		s.ready = append(s.ready, f)
		return
	}
	ch.sendQ = append(ch.sendQ, sendWaiter{f: f, v: v})
	s.setState(f, StateWaitChanSend,
		fmt.Sprintf("waiting to send on channel '%s' (no receiver, buffer full)", ch.Name))
}

func (s *Scheduler) handleRecv(f *Fiber, ch *Channel) {
	if len(ch.buf) > 0 {
		f.inbox = ch.buf[0]
		ch.buf = ch.buf[1:]
		if len(ch.sendQ) > 0 {
			w := ch.sendQ[0]
			ch.sendQ = ch.sendQ[1:]
			ch.buf = append(ch.buf, w.v)
			s.wake(w.f, fmt.Sprintf("buffered send on channel '%s' accepted", ch.Name))
		}
		s.setState(f, StateReady, fmt.Sprintf("received buffered value on channel '%s'", ch.Name))
		s.ready = append(s.ready, f)
		return
	}
	if len(ch.sendQ) > 0 {
		w := ch.sendQ[0]
		ch.sendQ = ch.sendQ[1:]
		f.inbox = w.v
		s.wake(w.f, fmt.Sprintf("send on channel '%s' received by fiber %d(%s)",
			ch.Name, f.ID, f.Name))
		s.setState(f, StateReady, fmt.Sprintf("received value directly on channel '%s'", ch.Name))
		s.ready = append(s.ready, f)
		return
	}
	ch.recvQ = append(ch.recvQ, f)
	s.setState(f, StateWaitChanRecv,
		fmt.Sprintf("waiting to receive on channel '%s' (no sender)", ch.Name))
}

// Mutex 是 Fiber 间的互斥锁。
type Mutex struct {
	Name  string
	owner *Fiber
	waitQ []*Fiber
}

// NewMutex 创建一个命名互斥锁。
func NewMutex(name string) *Mutex {
	return &Mutex{Name: name}
}

func (s *Scheduler) handleLock(f *Fiber, mu *Mutex) {
	if mu.owner == nil {
		mu.owner = f
		s.setState(f, StateReady, fmt.Sprintf("acquired lock '%s'", mu.Name))
		s.ready = append(s.ready, f)
		return
	}
	mu.waitQ = append(mu.waitQ, f)
	s.setState(f, StateWaitLock,
		fmt.Sprintf("waiting for lock '%s' held by fiber %d(%s)",
			mu.Name, mu.owner.ID, mu.owner.Name))
}

func (s *Scheduler) handleUnlock(f *Fiber, mu *Mutex) {
	if mu.owner != f {
		s.setState(f, StateReady,
			fmt.Sprintf("unlock of lock '%s' ignored (not the owner)", mu.Name))
		s.ready = append(s.ready, f)
		return
	}
	if len(mu.waitQ) > 0 {
		w := mu.waitQ[0]
		mu.waitQ = mu.waitQ[1:]
		mu.owner = w
		s.wake(w, fmt.Sprintf("acquired lock '%s' released by fiber %d(%s)",
			mu.Name, f.ID, f.Name))
	} else {
		mu.owner = nil
	}
	s.setState(f, StateReady, fmt.Sprintf("released lock '%s'", mu.Name))
	s.ready = append(s.ready, f)
}
