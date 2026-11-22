// Package mtuinfer infers integer physical-link MTUs from signed tunnel probe
// receipts without enumerating payloads or MTU products.
//
// A Receipt is supplied after its cryptographic signature has been verified by
// the caller. Each receipt binds a generation, signed label, candidate route
// set, send length, and success/failure status. All receipts with the same
// generation and label use one selected route from the same candidate set.
//
// Success receipts establish lower MTU bounds from actual wire lengths.
// Packet-too-big receipts establish only the upper bound wireLength-1 on at
// least one traversed physical link; they never imply a zero or negative MTU.
// Generation changes isolate labels, while evidence remains reusable when
// routes explicitly reference the same physical LinkID.
//
// Analyze returns both a universal proof and an existential witness. Callers
// can independently replay them with VerifyResult. The solver enumerates only
// route selections (at most four candidates per signed label), never the
// payload/MTU Cartesian product.
package mtuinfer
