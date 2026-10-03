package versionset

// CompactL0ToL1 performs a full L0 -> L1 compaction in three phases:
//
//	1. WriteSST materializes the merged output file (durable on disk, but
//	   not yet referenced by any version).
//	2. A VersionEdit is built that deletes the L0 inputs and adds the L1
//	   output.
//	3. LogAndApply commits the edit atomically.
//
// A crash between phases 1 and 3 leaves an orphan SSTable on disk that
// recovery will delete; the pre-compaction version stays in effect.
func (vs *VersionSet) CompactL0ToL1(inputs []uint64, smallest, largest string, merged []byte) (FileMetadata, error) {
	meta, err := vs.WriteSST(smallest, largest, merged)
	if err != nil {
		return FileMetadata{}, err
	}
	edit := &VersionEdit{}
	for _, n := range inputs {
		edit.DeletedFiles = append(edit.DeletedFiles, DeletedFileEntry{Level: 0, Number: n})
	}
	edit.NewFiles = append(edit.NewFiles, NewFileEntry{Level: 1, Meta: meta})
	if err := vs.LogAndApply(edit); err != nil {
		return FileMetadata{}, err
	}
	return meta, nil
}
