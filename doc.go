// Package netsec audits and repairs the adjacency-model physical layout
// described in this exercise.
//
// A witness is an ordered triple (sensitive, attacker at row-1, attacker at
// row+1) sharing a channel and bank. Audit enumerates every such three-page
// triple. Repair relocates movable pages into supplied free frames, minimizing
// the number of moved pages and then applying a deterministic lexicographic
// tie-break.
//
// Intermediate migrations use the strict set interpretation: no witness triple
// may appear that was absent before a step. For a relocated page whose
// geometry changes, this is equivalent to requiring that the page not
// participate in a witness at its destination.
//
// This package models only the adjacency rule stated by the exercise. It does
// not estimate or claim to reproduce real DRAM disturbance rates.
package netsec
