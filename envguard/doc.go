// Package envguard constructs declarative environment values for a privileged
// launcher and proves that sensitive exports are safe before publication.
//
// # Trust model
//
// Every input binding carries an identity (tenant, name), a trust domain,
// and a regular-language automaton constraining its characters. Two kinds of
// origin are tracked independently for every output rune:
//
//   - Content origin: where the character itself comes from.
//   - Control origin: which bindings decide that the rune exists at this
//     position. A value whose characters are all trusted constants can still
//     be untrusted when an untrusted tenant selects which constant (or which
//     length) is emitted.
//
// Values are stored in a hash-consed, shared concatenation DAG. The same
// sub-rope can be referenced by many entries without copying, and ropes up
// to 2^44 runes are addressed by offset without materialization.
//
// # Expressions
//
// The graph builder supports literals, qualified variable references,
// bounded loops, condition branches, ASCII case folding, constant-bound
// substrings, concatenation, and host-declared sanitizers. Conditions may
// only test whether an input belongs to one of the declared regular
// languages; a condition whose two branches build to the identical node
// erases the selector's control dependency because it cannot change output.
//
// # Sanitizers
//
// A sanitizer declaration states the exact input/output relation:
//
//   - NewRemoveSanitizer deletes a declared rune set. It vouches for the
//     surviving content but cannot launder control: the untrusted input
//     still determines the output length and character placement.
//   - NewFixedSanitizer always emits a fixed number of runes. Because the
//     shape cannot vary with the input, it may also wash data control.
//     Selector (branch) control is always retained.
//
// # Export checks
//
// Publisher.Check enforces three independent constraints and returns the
// earliest offending output interval with the constraint kind:
//
//   - ViolationContent: a rune's content origin is untrusted.
//   - ViolationControl: a rune's presence depends on untrusted data or an
//     untrusted branch selector.
//   - ViolationPath: after POSIX segment normalization (folding repeated
//     slashes, "." and ".."), the path leaves the approved prefix set.
//
// The path proof is segment based and streams over the rope, so a 2^44-rune
// value is never expanded just to be checked.
package envguard
