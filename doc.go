// Package pathsec implements joint RPKI ROA, ASPA and BGPsec path
// validation plus bounded adversarial path search for Go routing
// control-plane components.
//
// The package has no network dependencies and uses only the Go standard
// library for Ed25519 and ECDSA-P256 verification.
package pathsec
