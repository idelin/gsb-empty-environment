// Package netsec provides settlement attribution for a card or terminal
// settlement service.
//
// A terminal's merchant, acquirer, settlement account, and currency are
// recorded as one signed binding. Before a settlement is accepted, the service
// checks every attribution field against that binding, validates merchant and
// account state and limits, and returns the signed account as the only payout
// destination. Caller-supplied destination details are never used to choose an
// account.
//
// Changing a terminal binding requires a two-stage workflow: a rebind proposal
// must be approved by a reviewer and remains pending for a configurable delay
// before it becomes effective. This prevents an immediate merchant or account
// swap from redirecting the next settlement.
//
// The reviewer identity passed to ApproveRebind must be authenticated by the
// hosting settlement service; this package records it but does not perform
// operator authentication.
package netsec
