// Package taskpkg implements bounded structural diffing for content block
// trees. Callers mark volatile regions (for example rotating ads) as ignored;
// the diff then reports matches, moves, deletes and inserts over the remaining
// body blocks, with source text spans and an optimality flag.
package taskpkg
