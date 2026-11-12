// Package taskpkg expands collected order documents with repeatable
// groups into flat rows. The caller supplies a Spec: per level it lists
// the array field paths, the stable element ID field, and a Zip or Cross
// combination rule. Null fields participate as one unknown element, empty
// arrays yield the empty set (zero rows at that level), Zip pads shorter
// sides with missing markers, and Cross builds the cartesian product.
// Every row carries its full element source path, and Plan/Publish turn
// full re-expansions into precise row-level retractions and additions for
// element inserts, deletes, moves and corrections. If an expansion exceeds
// the row budget, nothing is published.
package taskpkg
