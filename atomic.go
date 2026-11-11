package netsec

import "sync/atomic"

// atomicCredential holds a *Credential behind an atomic pointer so readers
// never observe a half-written value while another goroutine mutates a
// MutableSource.
type atomicCredential struct {
	value atomic.Pointer[Credential]
}

func (a *atomicCredential) Store(value *Credential) { a.value.Store(value) }

func (a *atomicCredential) Load() *Credential { return a.value.Load() }
