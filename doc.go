// Package linker implements a small, deterministic section-based linker core.
//
// It provides two policies that are deliberately kept independent:
//
//   - GCSections performs section-level --gc-sections reachability analysis.
//   - ICF performs --icf=safe / --icf=all identical-code folding using Moore
//     partition refinement over instruction bytes and relocation shape.
//
// Sections are nodes in the graph.  A relocation is an edge from the section
// containing the relocation to the section that defines the relocation's
// target symbol.  CALL and ADDR_TAKEN edges are both graph edges; ADDR_TAKEN
// additionally records that the target's address can be observed by the
// program.
//
// The package does not mutate its inputs.  Every public transformation returns
// a fresh Image and a result object containing the old-to-new section mapping.
package linker
