// Package netsec implements physician-signed prescription binding and one-time
// pharmacy dispensing.
//
// A Prescription cryptographically binds the prescription identifier, patient,
// prescriber, diagnosis, drug code, dose, unit, and validity interval. Before a
// fill the Dispenser re-verifies that signature and checks every supplied field,
// rejecting patient substitution, drug substitution, overdose, and replay.
package netsec
