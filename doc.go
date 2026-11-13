// Package netsec provides COSEM association and access-control decisions for a
// Go meter front end.
//
// The controller fails closed: public clients may only read registered
// objects, and management clients may invoke class 70 methods 1 or 2 only
// after completing HLS challenge-response on the current association.
// Releasing or replacing an association clears its authentication state.
package netsec
