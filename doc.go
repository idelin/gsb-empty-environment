// Package netsec provides an importable, streaming expansion budget for
// attacker-controlled nested container trees.
//
// A tree is made of *Node values. Stream nodes expose a DecodeFunc that writes
// through a controlled io.Writer; container nodes expose children. Expansion
// plans the whole tree first, rejects cycles before running any decoder, and
// then decodes leaves concurrently while enforcing byte, depth, leaf, and live
// staging-buffer budgets. No result is exposed unless every referenced leaf
// succeeds.
package netsec
