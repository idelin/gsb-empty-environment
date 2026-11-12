// Package netsec provides the receive-path key state guard for a Thread
// protocol stack. It rejects key-sequence skips, switches during the caller's
// protection period, replayed frame counters, and attempts to reuse retired
// key sequences. Frame authentication itself remains the caller's
// responsibility.
package netsec
