// Package netsec verifies multi-point polynomial commitment openings for a
// trusted host. All field, group, pairing and canonical-codec operations are
// supplied by the host; the verifier request contains only C, (z, y) pairs and
// pi, never an SRS.
package netsec
