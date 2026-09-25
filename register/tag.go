package register

// Tag totally orders register versions. Two tags are compared first by
// version number and then by the unique writer ID, so any two tags
// produced by concurrent writers are always comparable and never equal:
// the pair (Ver, Writer) is unique per write.
type Tag struct {
	Ver    int
	Writer int
}

// Less reports whether t orders before u.
func (t Tag) Less(u Tag) bool {
	if t.Ver != u.Ver {
		return t.Ver < u.Ver
	}
	return t.Writer < u.Writer
}
