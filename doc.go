// Package netsec computes worst-case simultaneously-live stack usage for a
// finite call graph of fixed-size frames. Recursive cycles must decrease an
// integer caller parameter; cycles whose total change is not strictly negative
// are reported as unknown.
//
// Each function has one logical integer parameter. An Edge adds Delta to the
// caller parameter to obtain the callee parameter and may carry a Guard on the
// caller parameter. By default the parameter is a non-negative depth budget:
// calls are considered only while both caller and callee parameters are
// non-negative, so a strictly decreasing cycle is well-founded. Set
// Options.AllowNegativeParameters for arbitrary integers; in that mode every
// cycle also needs a terminating lower-bound guard.
//
// Normal calls keep the caller frame alive; tail calls release it before
// entering the callee. Returned calls release all frames allocated by the
// callee, so sibling calls take the maximum of their demands rather than the
// sum.
//
// Analyze returns the worst simultaneous byte count from the entry call,
// per-call transitive stack requirements, and a witness path that attains the
// computed peak.
package netsec
