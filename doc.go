// Package netsec provides offline time mutation for protocol handshake
// testing.
//
// The package works purely on integer event timestamps and their partial
// order constraints. It never opens sockets or sends network traffic: a
// test host feeds it a [Problem] (events with integer bounds plus ordering
// constraints), and it returns legal timestamp assignments ([Mutation])
// together with the deterministic seeds that reproduce them.
//
// The core model is a system of difference constraints. Every ordering and
// bound is rewritten into an inequality of the form t[v] - t[u] <= w and
// solved with Bellman-Ford. A negative cycle means the constraints are
// contradictory; [CycleError] exposes that cycle as evidence. Small integer
// time domains can be fully explored with [Enumerate] so tests can
// independently verify coverage and legality.
package netsec
