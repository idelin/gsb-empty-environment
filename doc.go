// Package netsec verifies X.509 certificate paths with DSA parameter
// inheritance.
//
// DSA keys use domain parameters (p, q, g). For a subordinate certificate the
// parameters may be omitted, in which case they are inherited from the issuing
// certificate. If they are present they must be identical to the parameters of
// the issuer. A mid-chain parameter replacement is rejected.
package netsec
