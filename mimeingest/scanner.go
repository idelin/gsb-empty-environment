package mimeingest

import "io"

// Scanner inspects attachment payloads as they are decoded.
type Scanner interface {
	// Begin starts scanning one attachment. Decoded bytes are written
	// to the returned Session as they stream by.
	Begin(meta Attachment) (Session, error)
}

// Session receives the decoded bytes of a single attachment.
type Session interface {
	io.Writer
	// Verdict is called after the final byte has been written. A
	// non-nil error rejects the attachment and aborts the ingest.
	Verdict() error
}
