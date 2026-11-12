// Package netsec provides an HTTP/2 server-push admission controller for a
// forwarding proxy.
//
// A proxy multiplexes many authenticated client requests over one connection,
// and each upstream PUSH_PROMISE must only be honored within the
// authorization context of the client request that triggered it. Without an
// explicit admission step, a promise associated with one origin can borrow
// the credentials or routing authority established for another origin on the
// same connection. This package makes that boundary enforceable.
//
// # Binding
//
// Every client request is registered with BindParent together with the exact
// set of origins its authentication context authorizes. Every promise is then
// bound to three things:
//
//   - the parent request stream that owns the authorization,
//   - the connection generation (physical connection incarnation), and
//   - the canonical origin asserted by the promised request.
//
// # Validation before allocation
//
// AdmitPromise validates the full pseudo-header block (RFC 7540 sections
// 8.1.2 and 8.2: mandatory :method/:scheme/:authority/:path, ordering,
// duplicates, safe methods, connection-specific headers, host consistency, and
// a canonicalizable authority) before any per-stream state is created. A
// malformed promise therefore cannot reserve a stream identifier, grow the
// controller's maps, or reach the business handler.
//
// # Cancellation and reuse
//
// Parent-request cancellation (CancelParent) and pushed-stream cancellation
// (CancelPush) are recorded as separate events and counters and are both
// idempotent. A promised stream identifier cannot be reused within one
// connection generation; reuse is permitted only after AdvanceGeneration,
// which simultaneously invalidates every parent and admission from prior
// incarnations so late or replayed PUSH_PROMISE frames are refused.
//
// # Delivery
//
// Only an Admission returned by a successful AdmitPromise can be delivered,
// and Delivery re-checks liveness under the lock so the Handler can never
// observe a resource whose parent or push stream was canceled, or whose
// generation is stale. The Handler is invoked outside the controller lock.
package netsec
