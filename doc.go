// Package netsec implements single-use pre-authorized codes with an optional,
// per-code transaction code (tx_code) binding.
//
// It is an in-process, transport-independent library intended to be imported
// by a credential issuance service that implements the OpenID for Verifiable
// Credential Issuance pre-authorized code flow. The caller is responsible for
// authenticating the end user, delivering the transaction code over a separate
// channel, and exposing the returned errors as protocol-level OAuth errors.
package netsec
