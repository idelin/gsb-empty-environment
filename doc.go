// Package netsec proves integer array indices in bounds for a safe
// compilation service. The host builds an integer control-flow graph in
// which assignments are limited to x += c and guards are lower/upper
// bounds on the difference of two variables. Analysis propagates closed
// difference bound matrices, widening at loop heads and then narrowing.
// Every array access is reported Proven or unknown together with the
// constraint basis justifying the verdict, and a bounded concrete
// interpreter double-checks all proven accesses.
package netsec
