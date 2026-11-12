// Package netsec models a generation-safe debugger attach transaction.
//
// A debugging agent pauses a process and reads its registers. Because numeric
// process and thread identifiers are reused by the operating system, a naive
// client that keys state only on a PID can accidentally read registers of a
// new tenant that took over a recycled PID.
//
// The package binds each attach transaction to a trusted process generation
// (see ProcHandle), a generation-bound debug authorization (see Auth) and the
// set of target threads (see AttachTx). Register reads are permitted only after
// a barrier confirms a same-generation stop for every target thread, including
// threads created while the transaction is in progress. On failure the
// transaction resumes precisely the threads it suspended itself, never stops
// owned by a concurrent tracer. Kernel is an in-memory model kernel used to
// deterministically exercise exit, identifier reuse and late-confirmation
// races.
package netsec
