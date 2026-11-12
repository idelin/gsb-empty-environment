package netsec

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
)

// Commit re-verifies every staged partition (size and digest) and only then
// writes the verified bytes to device, each at its declared destination
// interval.
//
// Because Prepare never touches the device, a package rejected for bad
// ranges, protected overlap, budget, length or digest guarantees zero device
// writes. Within Commit itself, all rechecks run before the first WriteAt, so
// a recheck failure likewise leaves the device untouched.
func (update *StagedUpdate) Commit(device Device) error {
	if device == nil {
		return fmt.Errorf("%w: target device is nil", ErrInvalidConfig)
	}
	if update.closed {
		return ErrClosed
	}

	// Phase 1: re-verify everything offline. No device call happens until this
	// loop completes without error, so a corrupt last item cannot leave a
	// partially written earlier partition behind.
	for _, item := range update.items {
		if err := recheck(item); err != nil {
			return err
		}
	}

	// Phase 2: publish verified bytes to the device in manifest order.
	for _, item := range update.items {
		if _, err := item.file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("%w: partition %q: %v", ErrDeviceWrite, item.part.Name, err)
		}
		writer := &deviceWriterAt{device: device, offset: item.part.Dest.Offset}
		n, err := io.Copy(writer, item.file)
		if err != nil {
			return fmt.Errorf("%w: partition %q: %v", ErrDeviceWrite, item.part.Name, err)
		}
		if n != item.part.Dest.Length {
			return fmt.Errorf("%w: partition %q: wrote %d of %d bytes",
				ErrDeviceWrite, item.part.Name, n, item.part.Dest.Length)
		}
	}
	return nil
}

// recheck re-reads a staged file and compares its on-disk size and digest
// against the manifest. It detects tampering or truncation of the isolated
// storage between Prepare and Commit.
func recheck(item *stagedItem) error {
	info, err := item.file.Stat()
	if err != nil {
		return fmt.Errorf("%w: partition %q: %v", ErrCommitRecheck, item.part.Name, err)
	}
	if info.Size() != item.part.Dest.Length {
		return fmt.Errorf("%w: partition %q: size %d, want %d",
			ErrCommitRecheck, item.part.Name, info.Size(), item.part.Dest.Length)
	}

	hasher, err := newHasher(item.part.HashAlgo)
	if err != nil {
		return fmt.Errorf("%w: partition %q: %v", ErrCommitRecheck, item.part.Name, err)
	}
	if _, err := item.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("%w: partition %q: %v", ErrCommitRecheck, item.part.Name, err)
	}
	if _, err := io.Copy(hasher, item.file); err != nil {
		return fmt.Errorf("%w: partition %q: %v", ErrCommitRecheck, item.part.Name, err)
	}
	if subtle.ConstantTimeCompare(hasher.Sum(nil), item.part.ExpectedHash) != 1 {
		return fmt.Errorf("%w: partition %q", ErrCommitRecheck, item.part.Name)
	}
	return nil
}

// deviceWriterAt adapts a Device (io.WriterAt) into an io.Writer whose writes
// advance through the partition's destination interval.
type deviceWriterAt struct {
	device Device
	offset int64
}

func (w *deviceWriterAt) Write(p []byte) (int, error) {
	n, err := w.device.WriteAt(p, w.offset)
	w.offset += int64(n)
	return n, err
}

// Close removes all isolated staging files and the staging directory. It is
// safe to call multiple times and should always be deferred by the update
// host, whether Commit ran or not.
func (update *StagedUpdate) Close() error {
	if update.closed {
		return nil
	}
	update.closed = true
	return update.closeStaging()
}

func (update *StagedUpdate) closeStaging() error {
	var errs []error
	for _, item := range update.items {
		if item.file != nil {
			if err := item.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				errs = append(errs, err)
			}
		}
	}
	if update.dir != "" {
		if err := os.RemoveAll(update.dir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
