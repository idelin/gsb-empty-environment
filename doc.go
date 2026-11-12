// Package netsec provides admission-time verification of SLSA v1
// provenance statements.
//
// The package is deliberately strict about the fields that a submitter can
// influence. A statement is accepted only when its builder is in the caller's
// trusted table, its build type is the one pinned for that builder, its
// external parameters are an exact subset of the registered keys, it contains
// no internal parameters, and a resolved dependency carries the declared
// source commit in its digest.
package netsec
