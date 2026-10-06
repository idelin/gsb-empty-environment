package publisher

// Manifest is the versioned root readers use to find a consistent set of
// virtual files. It is installed by an atomic rename, so readers always see
// either the whole old manifest or the whole new one, never a mix.
type Manifest struct {
	Version uint64              `json:"version"`
	Files   map[string]FileMeta `json:"files"`
}

type FileMeta struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
