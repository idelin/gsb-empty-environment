// Package recovery provides a receipt-driven recovery coordinator and a
// single-replica executor for replaying a fixed, finite batch of transactions
// after a primary failure.
//
// # Model
//
// A Problem lists at most ten transactions, at most six keys and at most
// twelve candidate starting sites. Each site is a concrete persisted world:
// a committed-transaction bitmap plus exact key/value state. The real site is
// unknown and the coordinator is never given its bitmap.
//
// Transactions belong to groups. A Command replays an ordered prefix of one
// group; transactions execute one at a time and atomically. A transaction
// already committed is deduplicated by ID: its read predicate is not rerun
// and its write is not repeated. An uncommitted transaction executes only if
// its complete half-open interval reads hold and its group predecessor is
// committed; otherwise the command stops before it and returns a
// RejectReceipt carrying the rejected ID.
//
// A Probe reliably returns only the parity (even/odd) of the committed count
// of one group. Probes and commands both cost a positive fee; command fees are
// never refunded for a rejection or a disconnect.
//
// At most one disconnect can occur, at any breakpoint including before the
// first and after the last transaction. A DisconnectReceipt discloses only
// the disconnect, never how many transactions executed. Redelivery is safe
// because committed transactions dedupe by ID and nothing before a failure is
// ever rolled back.
//
// # Strategy synthesis
//
// Solve builds the finite belief-state game over all worlds consistent with
// the observed receipts and computes the minimum worst-case total fee with a
// monotone minimax worklist algorithm. Completion is announced only when
// every compatible world equals both the target bitmap and the exact target
// key/value state.
//
// When no strategy can force the target, Solve returns a Certificate. The
// certificate names a closed losing region containing the initial belief and,
// for every sendable action at every covered belief, one genuinely possible
// receipt whose remaining belief stays inside the region. Verify replays each
// witness independently, so the impossibility proof is checkable without
// trusting the planner's internal labels.
package recovery
