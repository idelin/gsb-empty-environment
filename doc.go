// Package taskpkg computes a deterministic reading order for visible text
// blocks extracted from multi-column documents.
//
// Callers supply each block's rectangle, explicitly marked heading state, and
// explicit DOM ancestor IDs. Column dividers are candidates rather than
// certainties: the package enumerates every non-degenerate subset and chooses
// the partition that crosses the fewest non-heading blocks. Ordering inside a
// column is vertical, while explicit ancestor relationships take precedence
// over geometric hints.
package taskpkg
