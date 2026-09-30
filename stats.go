package sstdict

// Stats accumulates read-path counters for a snapshot or store.
type Stats struct {
	RandomReads       uint64 // number of Get calls served
	MaxDecodedPerRead int    // max entries decoded by a single Get
	PrefixScans       uint64 // number of PrefixRange calls served
	ScanBlocksDecoded uint64 // blocks decoded while scanning
	ScanEntries       uint64 // entries decoded while scanning
}

func (s *Stats) observeRandomRead(decoded int) {
	s.RandomReads++
	if decoded > s.MaxDecodedPerRead {
		s.MaxDecodedPerRead = decoded
	}
}

func (s *Stats) observeScan(entries int) {
	s.PrefixScans++
	s.ScanBlocksDecoded++
	s.ScanEntries += uint64(entries)
}
