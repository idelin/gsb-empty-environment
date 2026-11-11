package cdcupload

import "errors"

var (
	// ErrMinSizeTooSmall indicates MinSize is below the supported floor.
	ErrMinSizeTooSmall = errors.New("cdcupload: MinSize must be at least 16 bytes")
	// ErrAvgBelowMin indicates AvgSize is smaller than MinSize.
	ErrAvgBelowMin = errors.New("cdcupload: AvgSize must be >= MinSize")
	// ErrMaxBelowAvg indicates MaxSize is smaller than AvgSize.
	ErrMaxBelowAvg = errors.New("cdcupload: MaxSize must be >= AvgSize")

	// ErrSourceChanged indicates the source file changed between resumable
	// runs: its size or whole-file digest no longer matches the session.
	ErrSourceChanged = errors.New("cdcupload: source file changed since the session was created")

	// ErrSessionCorrupt indicates the persisted session state is inconsistent.
	ErrSessionCorrupt = errors.New("cdcupload: corrupt upload session state")

	// ErrIntegrity indicates a reassembled file does not match its manifest.
	ErrIntegrity = errors.New("cdcupload: integrity check failed")
)
