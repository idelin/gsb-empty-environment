// Package netsec plans labelled memory allocations over address segments.
//
// Labels are finite and local to planned address ranges. Releasing an
// allocation records the released address subrange and places its label in
// quarantine until a virtual-clock expiry. A label becomes reusable at the
// expiry instant, not one tick later. Splitting and merging free segments
// preserve and clip these records so an old address and label pair cannot
// become valid again through a boundary change.
package netsec
