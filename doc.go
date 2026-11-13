// Package netsec validates certificate policies along X.509 certification
// paths using the algorithms in RFC 5280 and RFC 9618.
//
// The policy engine implements inhibitAnyPolicy boundary semantics: anyPolicy
// may synthesize missing policy nodes only while the inhibitor is positive.
// After it reaches zero, anyPolicy cannot create new concrete policy nodes,
// while nodes and mappings created at earlier depths remain in the graph.
package netsec
