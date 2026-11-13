// Package netsec implements a delayed, multi-factor state machine for changing
// account recovery methods.
//
// A change is bound to the account, the active configuration digest, the
// requesting session, the target method, and a random request ID. Once the
// required independent strong factors have signed the complete request, the
// new entry becomes Pending. It activates only after the caller-supplied policy
// delay and only if the confirmer entries, configuration, and session are
// still valid at that time.
package netsec
