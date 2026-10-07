// Package tail implements a polling log tailer that survives truncation,
// rename rotation and same-path replacement of the tailed file.
//
// Cursor management: the tailer tracks the open file by identity
// (device + inode) and a byte offset within it. Bytes are only published
// once a terminating newline arrives; a trailing partial line is buffered
// across polls. When the path starts pointing at a different identity
// (rotation/replacement), the old handle is drained to EOF and closed
// before the new file is opened. When the current file shrinks below the
// cursor (truncation), that file enters a new generation and is re-read
// from offset 0. Each generation is numbered; a complete line is emitted
// exactly once for the generation it belongs to.
package tail

import (
	"bytes"
	"io"
	"os"
	"syscall"
)

// FileID identifies a physical file independent of its path.
type FileID struct {
	Dev int64
	Ino uint64
}

// Line is one complete newline-terminated line.
type Line struct {
	Gen  int    // generation of the file the line was read from
	Text string // line content without the trailing newline
}

// Tailer follows a single path across rotations, truncations and removals.
type Tailer struct {
	path string
	emit func(Line)

	file    *os.File
	id      FileID
	open    bool
	offset  int64
	gen     int
	partial []byte

	nOpen int // currently open handles, for leak detection
}

// New returns a Tailer for path, publishing complete lines to emit.
func New(path string, emit func(Line)) *Tailer {
	return &Tailer{path: path, emit: emit}
}

// Gen returns the current generation number.
func (t *Tailer) Gen() int { return t.gen }

// OpenHandles returns the number of file handles currently held open.
func (t *Tailer) OpenHandles() int { return t.nOpen }

func fileID(fi os.FileInfo) FileID {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return FileID{Dev: int64(st.Dev), Ino: st.Ino}
	}
	return FileID{}
}

// Sync performs one poll cycle: it reconciles the path with the current
// handle (draining and closing stale handles first) and reads all bytes
// newly available, emitting every complete line among them.
func (t *Tailer) Sync() error {
	fi, err := os.Stat(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Path is gone: drain and release the old handle.
			return t.retire()
		}
		return err
	}
	id := fileID(fi)
	if t.open && id != t.id {
		// Rotated or replaced: finish the old file before moving on.
		if err := t.retire(); err != nil {
			return err
		}
	}
	if !t.open {
		f, err := os.Open(t.path)
		if err != nil {
			return err
		}
		t.file = f
		t.id = id
		t.open = true
		t.nOpen++
		t.gen++
		t.offset = 0
		t.partial = t.partial[:0]
	} else if fi.Size() < t.offset {
		// Truncated: same handle, new generation, read from the start.
		t.gen++
		t.offset = 0
		t.partial = t.partial[:0]
	}
	return t.readAvail(t.file)
}

// retire drains the current handle to EOF and closes it. A partial line
// left at EOF is dropped: it can never be completed on a stale handle.
func (t *Tailer) retire() error {
	if !t.open {
		return nil
	}
	if err := t.readAvail(t.file); err != nil {
		return err
	}
	err := t.file.Close()
	t.nOpen--
	t.open = false
	t.file = nil
	t.partial = t.partial[:0]
	return err
}

// readAvail reads from the cursor to EOF, advancing the offset past every
// byte consumed and emitting the complete lines among them.
func (t *Tailer) readAvail(f *os.File) error {
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			t.offset += int64(n)
			t.consume(buf[:n])
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// consume splits p into complete lines, emitting each with the current
// generation, and buffers any trailing partial line.
func (t *Tailer) consume(p []byte) {
	for {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			t.partial = append(t.partial, p...)
			return
		}
		line := string(append(t.partial, p[:i]...))
		t.partial = t.partial[:0]
		t.emit(Line{Gen: t.gen, Text: line})
		p = p[i+1:]
	}
}

// Close releases the current handle, if any.
func (t *Tailer) Close() error {
	if !t.open {
		return nil
	}
	err := t.file.Close()
	t.nOpen--
	t.open = false
	t.file = nil
	return err
}
