// Package taskpkg computes deterministic reading order for visible text blocks
// in multi-column web pages.
//
// The input contains block rectangles, explicit heading markers, DOM ancestor
// paths or parent relations, writing direction, and at most six candidate
// vertical divider lines. Plan enumerates every candidate-line subset (at most
// 64), minimizes the number of distinct non-heading blocks crossed by selected
// lines, and then orders blocks by column progression, vertical position,
// explicit DOM structure, and original node ID.
//
// Coordinates are used only for rectangle intersection, column geometry, and
// vertical ordering. The package never uses nearest-coordinate neighbors as a
// substitute for the supplied structural constraints.
package taskpkg
