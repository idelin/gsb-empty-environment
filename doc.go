// Package netsec compiles firmware update region descriptors into the actual
// physical write set so that aliasing descriptions cannot hide writes into
// protected ranges such as the boot block.
//
// Address formation uses exact integer arithmetic only: every descriptor
// start is computed as Base + Offset*Scale with math/big, and every range
// check happens on the exact result. No native-width arithmetic is performed
// on user-controlled addresses, so an overflowing start can never silently
// wrap into a small boot address.
//
// Two wrap policies are supported:
//
//   - Strict (default): any negative start, or any run leaving the firmware
//     image, rejects the descriptor with a CompileError.
//   - ModularDecode: models hardware that truncates addresses through the
//     representation modulus 2^bits and then through the image-size modulus D
//     (requiring D to be a power of two dividing 2^bits). Every wrap layer is
//     reported on DescriptorResult instead of rejecting the descriptor:
//     Underflow for a negative exact start, RepresentationWrap for crossing
//     2^bits, DomainWrap for crossing an image boundary, and NonInjective when
//     the run is longer than D so two logical offsets hit one physical byte.
//
// The normalized write set is sorted and pairwise disjoint. Each Segment
// carries the full, de-duplicated chain of original descriptor names that
// write into it, which is the evidence needed when several descriptions alias
// one physical range. Intersecting that write set with the protected regions
// yields Violations whose descriptor chain names every responsible original
// description, including aliasing ones a per-descriptor check would miss.
//
// Correctness of the wrap and alias logic is established independently by
// exhaustive tests over reduced address domains (white-box option
// withRepresentationBits): every descriptor tuple for W=2 and W=3 domains,
// plus every descriptor pair for W=2 and sampled W=4 pairs, is checked
// against a byte-at-a-time oracle that simulates both moduli directly.
package netsec
