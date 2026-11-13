// Package netsec models page deduplication and copy-on-write timing probes in
// an isolation test host.
//
// A Memory merges pages only when their digests match and a MergePolicy
// authorizes the merge.  The conservative default requires the same domain,
// no sensitive tags, and explicit shareability on both pages.  The deliberately
// insecure GlobalDedupPolicy can be used to reproduce cross-domain probes.
//
// The experiment helpers enumerate a finite candidate secret set, run attacker
// Prime/Write sequences, group secrets by observed cost traces, and return the
// shortest distinguishing sequence.  CostModel uses inclusive intervals so a
// report can distinguish a definite leak from a possible leak when noise
// intervals overlap.
package netsec
