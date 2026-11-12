package netsec

// PageID identifies a simulated physical page owned by a Pool.
type PageID uint64

// Generation identifies a mapping epoch. Submissions must carry the current
// generation; a device reset advances it, invalidating older mappings.
type Generation uint64

// OpID identifies one in-flight DMA operation tracked by a Tracker.
type OpID uint64
