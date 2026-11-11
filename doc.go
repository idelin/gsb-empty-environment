// Package netsec compares finite protocol-execution traces for partial-order
// equivalence.
//
// Two traces are equivalent when they contain the same events (same subjects,
// object accesses and send/receive causality edges) and every pair of events
// that is dependent keeps the same relative order. A pair is dependent when
// the events conflict on an object (both touch it and at least one writes,
// which explicitly includes writes to secret state) or when one happens
// before the other through per-subject program order and send/receive
// causality edges. Equivalent traces differ only in the interleaving of
// independent events; Compare emits a certificate of adjacent independent
// swaps transforming one ordering into the other. Non-equivalent traces
// return violations that localize the necessary-order difference, including a
// happens-before witness chain.
package netsec
