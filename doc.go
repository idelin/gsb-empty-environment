// Package fparse is a unification-based chart parsing library for text
// annotators whose nonterminals carry finite feature maps. Feature values
// are constants or rule-local variables.
//
// A grammar holds at most 20 epsilon-free productions in the text form
//
//	S[mode=M] -> NP[case=nom num=N] VP[num=N mode=M]
//	NP -> Det N
//	N -> "dog"
//
// where bracketed pairs are feature constraints, values starting with an
// uppercase letter are rule-local variables, and quoted strings are
// terminal words. Inputs are word sequences of at most 20 words.
//
// ParseForest returns the shared (packed) forest of all unifiable
// analyses:
//
//   - Variables are renamed fresh on every rule instantiation and on
//     every read of a stored node, so branches of the search can never
//     pollute each other's substitutions.
//   - A duplicate feature key whose values do not unify fails the
//     analysis.
//   - Recursive rules (left or right) are supported by bottom-up
//     fixed-point chart parsing.
//   - Forest nodes are deduplicated by canonical (alpha-equivalence
//     normalised) feature maps, so equivalent substitutions share one
//     packed node.
//
// Forest.Trees expands the packed forest into individual trees and
// Forest.Verify independently re-checks each tree's word order and
// feature consistency by replaying unification from the leaves.
package fparse
