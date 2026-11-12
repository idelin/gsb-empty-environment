// Package netsec implements the certificate signing policy consulted by the
// approval controller before a CertificateRequest is handed to an issuer.
//
// The policy takes the parsed CSR content, the authenticated requester
// identity and the issuer policy, and verifies that:
//
//   - the CSR is consistent with the SAN/key fields declared on the
//     Certificate resource (tampered requests are rejected);
//   - DNS names (including the CN when it is DNS shaped) stay inside the
//     suffixes configured for the requester namespace, with label-boundary
//     suffix matching and leftmost-only wildcards;
//   - SPIFFE IDs carried in URI SANs are matched against trust domains
//     independently of the DNS suffix rules;
//   - CA basic constraints, validity duration, key strength/curve and key
//     usages comply with the issuer policy.
package netsec
