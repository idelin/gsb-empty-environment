package auditlog

import (
	"errors"
	"os"
	"path/filepath"
)

// Append writes and fsyncs one authenticated record. It returns the assigned
// sequence number. A failed Write or Sync freezes the writer: the caller must
// reopen after checking the on-disk state, so an uncertain append is never
// retried blindly on top of torn evidence.
func (w *Writer) Append(payload []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errors.New("auditlog: writer is closed")
	}
	if w.failed {
		return 0, errors.New("auditlog: writer is failed; reopen before appending")
	}
	if w.active == nil {
		return 0, errors.New("auditlog: no active segment")
	}
	if len(payload) > maxPayloadSize {
		return 0, errors.New("auditlog: payload exceeds size limit")
	}

	seq := w.nextSeq
	prevDigest := w.active.lastDigest
	if w.active.records == 0 {
		prevDigest = w.active.firstPrevDigest
	}
	frame := encodeRecord(seq, prevDigest, payload, w.keys.HMAC)
	if _, err := w.active.file.Write(frame); err != nil {
		w.failed = true
		return 0, err
	}
	if err := w.active.file.Sync(); err != nil {
		w.failed = true
		return 0, err
	}

	if w.active.records == 0 {
		w.active.firstSeq = seq
	}
	w.active.records++
	w.active.lastSeq = seq
	w.nextSeq = seq + 1
	w.active.lastDigest = recordDigest(frame)

	if w.active.records >= w.cfg.maxRecords {
		if err := w.rotateLocked(); err != nil {
			return seq, err
		}
	}
	return seq, nil
}

// Rotate seals the active segment immediately and opens a fresh segment.
func (w *Writer) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("auditlog: writer is closed")
	}
	if w.failed {
		return errors.New("auditlog: writer is failed; reopen before rotating")
	}
	return w.rotateLocked()
}

func (w *Writer) rotateLocked() error {
	if w.active == nil {
		return nil
	}
	if err := w.sealLocked(w.active); err != nil {
		w.failed = true
		return err
	}
	sealed := w.active
	return w.startSegment(sealed.index+1, sealed.sealDigest, sealed.lastDigest)
}

// sealLocked appends the independently signed seal, fsyncs it, closes the
// file and renames the segment to its sealed name.
func (w *Writer) sealLocked(segment *activeSegment) error {
	if segment.sealed {
		return nil
	}

	lastRecordDigest := genesisDigest[:]
	if segment.records > 0 {
		lastRecordDigest = segment.lastDigest
	}
	frame := encodeSeal(
		segment.index,
		uint64(segment.records),
		segment.firstSeq,
		segment.lastSeq,
		segment.prevSealDigest,
		lastRecordDigest,
		w.keys.Sign,
	)
	if _, err := segment.file.Write(frame); err != nil {
		return err
	}
	if err := segment.file.Sync(); err != nil {
		return err
	}
	if err := segment.file.Close(); err != nil {
		return err
	}

	oldPath := segment.file.Name()
	newPath := filepath.Join(w.dir, segmentName(segment.index, false))
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	if err := syncDir(w.dir); err != nil {
		return err
	}

	segment.sealDigest = sealDigest(frame)
	segment.sealed = true
	return nil
}

// Close seals and finalizes the active segment. An empty segment is removed
// instead of producing a pointless sealed file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true

	segment := w.active
	w.active = nil
	if segment == nil {
		return nil
	}

	if segment.records == 0 && !segment.sealed {
		name := segment.file.Name()
		if err := segment.file.Close(); err != nil {
			return err
		}
		return os.Remove(name)
	}
	return w.sealLocked(segment)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
