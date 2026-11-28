package compaction

import "time"

// testConfig builds a small, fast-to-trigger engine configuration.
func testConfig() Config {
	return Config{
		NumLevels:          4,
		L0Trigger:          2,
		LevelTargetBytes:   []int64{0, 2000, 2000, 2000},
		MaxSubcompactions:  4,
		SubcompactionBytes: 60,
		Stall: StallThresholds{
			L0SoftDelayFiles:      3,
			L0HardStopFiles:       5,
			PendingSoftDelayBytes: 200,
			PendingHardStopBytes:  400,
			DelayStep:             time.Millisecond,
		},
	}
}

func putEntry(key UserKey, seq uint64, val string) Entry {
	return Entry{
		InternalKey: InternalKey{UserKey: key, Seq: seq, Kind: KindPut},
		Value:       []byte(val),
	}
}

func delEntry(key UserKey, seq uint64) Entry {
	return Entry{
		InternalKey: InternalKey{UserKey: key, Seq: seq, Kind: KindDelete},
	}
}

// levelEntries flattens all point entries of a level, preserving per-file
// order (files are already sorted for L >= 1).
func levelEntries(v *Version, level int) []Entry {
	var out []Entry
	for _, f := range v.Files(level) {
		out = append(out, f.Entries...)
	}
	return out
}

func fileByNumber(v *Version, num uint64) *SSTable {
	for level := range v.Levels {
		for _, f := range v.Files(level) {
			if f.FileNumber == num {
				return f
			}
		}
	}
	return nil
}

func containsFile(files []*SSTable, num uint64) bool {
	for _, f := range files {
		if f.FileNumber == num {
			return true
		}
	}
	return false
}
