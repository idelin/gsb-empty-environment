// Package netsec validates 5GS NAS Security Mode Commands on the UE side.
//
// The verifier enforces the registration-stage security checks of
// TS 33.501 section 6.7.2 and TS 24.501 section 5.4.2.3:
//
//   - An SMC must be integrity protected; its 4-octet NAS-MAC is recomputed
//     with K_nasint derived from K_AMF (TS 33.501 Annex A.9/B.2) and compared
//     in constant time.
//   - The replayed UE security capabilities IE must be byte-identical to the
//     capabilities the UE sent in the Registration Request, including nested
//     EPS NAS security algorithms. Any deleted or altered bit causes a SECURITY
//     MODE REJECT with cause #23.
//   - The null integrity algorithm 5G-IA0 is accepted only for emergency
//     registrations; otherwise the SMC is rejected with cause #24.
//
// NIA2 (AES-CMAC) and the HMAC-SHA-256 generic KDF are implemented with the
// standard library. NIA1/SNOW 3G and NIA3/ZUC can be added through
// RegisterIntegrityAlgorithm.
package netsec
