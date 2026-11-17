// Package netsec provides exact counting and unbiased sampling of sandbox
// loader layouts.
//
// Up to six labeled, aligned regions are placed inside a domain of at most
// 32 pages; reserved pages are forbidden and regions must not overlap. Legal
// layouts are counted exactly with memoized dynamic programming and ranked
// lexicographically. Sampling injects random bytes through a Stream and uses
// integer-level rejection sampling followed by unranking, so every legal
// layout is selected with identical probability without any rejection-based
// retry over layouts.
package netsec
