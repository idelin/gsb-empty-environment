// Package streamsel extracts text from very large HTML documents with a
// CSS-selector subset, without building the full document tree.
//
// The Matcher consumes HTML in arbitrary chunks and emits the text of each
// matching element as soon as the element closes, so peak memory depends on
// the element nesting depth rather than the document size. The package ships
// its own incremental HTML tokenizer and repairs unclosed tags, self-closing
// elements and implicit tag ends on the fly.
//
// Supported selector subset: tag, .class, #id, [attr], [attr=value], the
// universal selector *, and descendant / child (>) combinators.
//
// ParseDOM and QueryAll provide the naive build-the-whole-tree baseline used
// for correctness cross-checking and benchmarking.
package streamsel
