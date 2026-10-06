package logtail

import "bytes"

// Line is one complete (newline-terminated) line delivered by a Tailer.
type Line struct {
	Gen  uint64 // generation of the file the line came from
	Ino  Ino    // identity of the file the line came from
	Text string // line content without the trailing newline
}

// Tailer follows the file at one path across truncations, renames and
// same-path replacements.
//
// Cursor management is based on file identity plus offset:
//   - a different Ino at the watched path means rotation/replacement:
//     the old handle is drained to EOF and closed before the new file
//     is opened;
//   - a size smaller than the current offset means truncation: the file
//     enters a new generation and is re-read from offset zero.
//
// A trailing partial line is buffered and only published once its
// terminating newline arrives; if its generation ends first, the
// partial bytes are dropped and never published.
type Tailer struct {
	fs   *FS
	path string

	h       *Handle
	ino     Ino
	gen     uint64
	partial []byte

	// Lines accumulates every delivered line, in delivery order.
	Lines  []Line
	closed bool
}

func NewTailer(fs *FS, path string) *Tailer {
	return &Tailer{fs: fs, path: path}
}

// Poll synchronously catches the tailer up with the filesystem.
func (t *Tailer) Poll() {
	if t.closed {
		return
	}
	ino, size, ok := t.fs.Stat(t.path)
	switch {
	case !ok:
		// Path vanished (renamed away or removed): finish the old
		// handle, then wait for the path to reappear.
		t.drain()
	case t.h == nil:
		t.open(ino)
	case t.ino != ino:
		// Rotated or replaced at the same path: read the old handle
		// to EOF first, then start the new generation.
		t.drain()
		t.open(ino)
	case size < t.h.Offset():
		// Truncated: the file enters a new generation. Bytes past
		// the new end are gone, so there is nothing to drain.
		t.closeHandle()
		t.open(ino)
	}
	t.readAvailable()
}

func (t *Tailer) open(ino Ino) {
	h, err := t.fs.Open(t.path)
	if err != nil {
		return
	}
	t.h = h
	t.ino = ino
	t.gen++
	t.partial = t.partial[:0]
}

func (t *Tailer) emit(text string) {
	t.Lines = append(t.Lines, Line{Gen: t.gen, Ino: t.ino, Text: text})
}

// readAvailable consumes the handle up to EOF, publishing complete
// lines and buffering any unterminated remainder.
func (t *Tailer) readAvailable() {
	if t.h == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		n, err := t.h.Read(buf)
		t.partial = append(t.partial, buf[:n]...)
		for {
			i := bytes.IndexByte(t.partial, '\n')
			if i < 0 {
				break
			}
			t.emit(string(t.partial[:i]))
			t.partial = t.partial[i+1:]
		}
		if err != nil {
			return
		}
	}
}

// drain reads the current handle to EOF (publishing its remaining
// complete lines), drops any unterminated partial line, and closes it.
func (t *Tailer) drain() {
	if t.h == nil {
		return
	}
	t.readAvailable()
	t.closeHandle()
}

func (t *Tailer) closeHandle() {
	if t.h == nil {
		return
	}
	t.h.Close()
	t.h = nil
	t.partial = t.partial[:0]
}

// Close releases the current handle, if any. Pending partial-line
// bytes are discarded.
func (t *Tailer) Close() error {
	t.closed = true
	t.closeHandle()
	return nil
}
