// Package adaptivehuffman implements byte-oriented adaptive prefix coding
// (FGK-style adaptive Huffman) for streaming sensor data: the input is
// encoded one byte at a time without any preliminary pass, and the encoder
// and decoder independently maintain identical adaptive code trees starting
// from the same initial state (a single escape/NYT node).
//
// A byte seen for the first time is transmitted as the escape (NYT) code
// followed by its raw 8 bits; afterwards it is encoded with its current
// Huffman code. Both sides apply the same deterministic update rules —
// stable node numbering, a highest-rank equal-weight swap rule, escape-node
// spawning, and weight rescaling on overflow — so the two trees never
// diverge. The update logic depends only on the tree structure and an
// explicit rank ordering, never on map iteration order.
//
// The package provides:
//   - symbol-level streaming (Encoder.EncodeSymbol / Decoder.DecodeSymbol
//     over BitWriter / BitReader);
//   - self-contained frames (EncodeFrame / DecodeFrame) whose header carries
//     the original length and a format/state version, with a fixed
//     zero-padding rule at the end;
//   - full state export/restore (ExportState / RestoreEncoder /
//     RestoreDecoder): resuming at any symbol boundary produces exactly the
//     same bit stream as an uninterrupted run.
//
// The wire format and all update rules are specified in FORMAT.md.
package adaptivehuffman
