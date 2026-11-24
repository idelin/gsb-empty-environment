// Package migrationadmission implements the migration admission checks for an
// identity directory that changes its name normalization rules.
//
// A tenant supplies, per policy version:
//
//   - a finite state grammar (DFA) describing the legal raw names
//     (names are byte strings of length at most MaxNameLength);
//   - a deterministic per-character transcription table where each consumed
//     character emits zero, one or two canonical symbols, plus an optional
//     tail symbol emitted when the end of the name is reached.
//
// The old and the new policy versions are bundled together with root-signed
// alias relations. Two different spellings may legitimately share an old
// subject. However, if two names that are legal under the new grammar produce
// the same new login identifier but have different old subjects, the pair must
// be covered by an alias claim signed by the tenant root for the exact policy
// version. Folding two non-alias names through an empty transcription
// (character deletion) or through an end-marker tail collision is rejected.
//
// The check is an algorithmic product-automaton search, not an enumeration of
// names. A separate lateness certificate declares the maximum number of
// canonical symbols that remain unmatched (buffered on either side, waiting
// for a counterpart) over every comparable prefix; values above
// MaxLateness are rejected and the declared bound is checked exactly.
// Character deletion, multi-symbol expansion, the end marker and input pairs
// of different lengths are all handled by the same construction.
//
// The signed Bundle binds both grammar/table versions, the tenant and the
// signing root. Login Credentials carry the bundle epoch, so rotating the
// alias set (a new bundle) invalidates every previously issued credential.
package migrationadmission
