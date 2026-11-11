// Package fat32 provides a read-only, random-access view over a FAT32 volume
// intended for forensic use on damaged or faulting storage.
//
// The central type is File, which implements io.ReaderAt over one file's
// cluster chain. Reads are served directly from the backing image through a
// ReaderAt; the whole image is never buffered, so opening multi-gigabyte
// evidence images costs O(1) memory and only one cluster is held at a time
// during a read.
//
// Both FAT copies are walked in lockstep. Clusters on which every copy agrees
// form a verified prefix. At the first disagreement the call returns the bytes
// that are identical under all copies together with an ErrFATMismatch whose
// ChainError reports the first ambiguous logical offset and cluster. Damage
// that is consistent across copies (cycles, bad/reserved/out-of-range
// pointers, dangling entries) is reported as ErrBadChain instead, since no
// ambiguity exists. A physically truncated image (a partial last cluster)
// yields every recoverable byte plus ErrTruncatedVolume.
//
// Short reads are stitched together transparently, and File carries no mutable
// cursor: a single File is safe for concurrent ReadAt use.
package fat32
