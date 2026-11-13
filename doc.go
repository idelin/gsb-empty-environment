// Package netsec validates 5G NAS Security Mode Command messages in a UE
// protocol stack.
//
// The package is intentionally focused on the security decisions that a UE
// must make when it receives a Security Mode Command during registration:
//
//   - the command must use the "integrity protected with new 5G NAS security
//     context" security header type and carry a NAS MAC;
//   - the NAS MAC is verified with KNASint derived from KAMF;
//   - the replayed UE security capabilities must be byte-for-byte identical to
//     the capabilities that the UE put in its Registration Request, including
//     the EPS capability octets;
//   - 5G-IA0 is accepted only for an emergency registration.
//
// Validation failures are returned as a Security Mode Reject decision that
// already contains the encoded NAS reject message.
package netsec
