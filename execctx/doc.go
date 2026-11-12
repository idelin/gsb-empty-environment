// Package execctx implements the per-request execution context used by the
// gateway when invoking untrusted detection plugins.
//
// Security model:
//
//   - One Controller owns the whole request. Every entry, including plugin
//     callbacks that reenter the request, passes through Controller.Enter and
//     is charged before any plugin code runs, so a recursive or reentrant
//     plugin cannot bypass the shared budget.
//   - Nested frames share the same budget, deadline and cancellation signal.
//     Frames implement context.Context but expose no way to cancel or reset:
//     cancellation is host-owned, idempotent, and cannot be cleared by a
//     sub-call.
//   - Asynchronous work must explicitly detach a Lease. The lease holds a
//     reservation and is the only thing that authorizes callbacks after the
//     originating frame has ended. Once the request is finished, all leases
//     are revoked, their reservations refunded, and any callback presenting
//     a released or unknown lease (or no lease at all) is rejected.
//   - Every frame settles at most once. Double completion returns ErrSettled
//     and never refunds twice; a refund larger than the frame charge returns
//     ErrBadRefund and leaves the frame active. Controller.Finish publishes a
//     single EndEvent.
package execctx
