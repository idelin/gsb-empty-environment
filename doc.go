// Package netsec implements single-use TPM policy-session transactions whose
// approved cpHash is bound to the exact command that reaches the TPM.
//
// TPM commands are supplied through Transport. An Authority tracks startup,
// numeric-handle generations, context save/load and reset. A Session
// serializes PolicyOR/PolicyNameHash/PolicyCpHash installation, freezes the
// command template, and remains reserved through final response validation.
// Unknown outcomes require quote/audit reconciliation and are never replayed
// automatically.
package netsec
