package publish

import (
	"errors"
	"os"
)

// Op identifies a fault-injectable step of the publish protocol.
type Op string

const (
	OpWriteDataFile Op = "write_data_file" // writing one staged data file
	OpWriteManifest Op = "write_manifest"  // writing the staged manifest
	OpValidate      Op = "validate"        // validating the staged directory
	OpRenameStage   Op = "rename_stage"    // staging/<id> -> versions/<id>
	OpWritePointer  Op = "write_pointer"   // writing the pointer tmp file
	OpRenamePointer Op = "rename_pointer"  // pointer tmp -> current
)

// ErrCrash aborts the publish as if the process died at that exact point.
// Re-open the store with Open to exercise crash recovery.
var ErrCrash = errors.New("publish: simulated crash (fault injection)")

// Hook is invoked before each fault-injectable operation. Returning a
// non-nil error aborts the publish with that error; returning ErrCrash
// specifically simulates an abrupt restart (no cleanup is performed).
type Hook func(op Op) error

// SetHook installs a fault-injection hook. Pass nil to clear it.
// Safe to call concurrently with Publish.
func (s *Store) SetHook(h Hook) {
	if h == nil {
		h = func(Op) error { return nil }
	}
	s.hook.Store(h)
}

func (s *Store) inject(op Op) error {
	if h, ok := s.hook.Load().(Hook); ok && h != nil {
		return h(op)
	}
	return nil
}

// writeFileSync writes a new file (must not exist) and fsyncs it.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// syncDir fsyncs a directory so renames/creations inside it are durable.
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
