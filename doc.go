// Package displaycommit implements display commitments for a small-screen
// hardware-wallet signing frontend.
//
// The package has four main layers:
//
//   - TypeTable and Message describe the normalized schema and signed message
//     tree, including field numbers, wire types, ordinal-preserving repeated
//     fields/lists, arbitrary-precision integers, addresses and restricted
//     text.
//   - DisplayRule, Render and VerifySmallDomain compare every screen in a
//     finite legal domain and prove that equal displays cannot differ on
//     payee/subject, asset, exact amount, signing domain or deadline.
//   - Interval represents closed amount ranges and rounding bins without
//     floating-point arithmetic.
//   - SigningRequest, PageProof, PagePacket and Device bind ordered pages,
//     table version, monotonic session nonce/counter and the raw message root,
//     and reject cross-transaction page-receipt replay after power loss.
//
// A nil or indeterminate verification result is represented by
// VerdictUnknown; callers must treat Unknown as "do not sign".
package displaycommit
