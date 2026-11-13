// Package netsec reconstructs interpreter-visible frames from optimized-frame
// metadata.
//
// The important security property of this package is object identity.  A
// virtual object identifier is materialized exactly once.  Every slot, field,
// and alias that refers to that virtual object is then rewritten to the same
// concrete object identifier.  In particular, virtual references are resolved
// only after the allocation phase has completed, so shared references and
// cycles cannot cause two objects to be created for one virtual identifier.
//
// Reconstruction is transactional.  If any allocation fails, every object
// allocated by that reconstruction is released in reverse allocation order,
// no frame is returned, and objects that already existed in the host heap are
// never released or modified.
package netsec
