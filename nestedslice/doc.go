// Package nestedslice implements zero-copy slicing for an Arrow-style but
// self-contained nested columnar layout: lists, structs, dense unions and
// dictionary arrays.
//
// The planner never materializes large regular selections. Top-level rows are
// arithmetic progressions (see AP) bundled into periodic sets (see PSet) and
// the periodic representation is propagated through every nesting level.
// A selection that can only be resolved by expanding an array larger than the
// configured limit fails with ErrIrregularTooLarge instead of expanding.
//
// Null semantics:
//   - A null parent row still owns a physical child interval (for example a
//     list offset span), but that interval contributes no logical values. The
//     planner records it as a shadow (forbidden) range on every descendant
//     buffer so over-reading can never leak it.
//   - An empty list is a valid row whose offsets are equal; it is distinct
//     from a null list.
//   - A null dense-union row may carry an illegal type id byte; the type id and
//     union offset of a null row are never read and are shadowed.
//
// Dictionary arrays reference a shared dictionary subtree. Multiple parent
// layers may select overlapping segments of the same dictionary without
// copying it; reversed selections are represented by negative-step APs and
// keep the parent's iteration order.
package nestedslice
