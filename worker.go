package main

import "sync"

// Handler processes one dequeued event.
type Handler func(Event)

// Pool runs a fixed set of workers pulling from the queue.
type Pool struct {
	q       *Queue
	handler Handler
	wg      sync.WaitGroup
}

func NewPool(q *Queue, h Handler) *Pool {
	return &Pool{q: q, handler: h}
}

func (p *Pool) Start(n int) {
	for i := 0; i < n; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for {
				ev, ok := p.q.Next()
				if !ok {
					return
				}
				p.handler(ev)
				p.q.MarkProcessed(ev)
			}
		}()
	}
}

// Wait blocks until all workers have exited.
func (p *Pool) Wait() {
	p.wg.Wait()
}
