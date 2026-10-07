package manifestdiff

import "fmt"

// Executor applies primitive operations to a tree. All paths are
// manifest-relative, slash-separated, and non-root.
type Executor interface {
	Stat(path string) (Entry, bool)
	Mkdir(path string) error
	WriteFile(path string, content []byte) error
	Symlink(path, target string) error
	Remove(path string) error
	Rename(src, dst string) error
}

// ContentStore resolves file digests to content for OpWriteFile.
type ContentStore interface {
	Get(digest string) ([]byte, error)
}

// MapStore is a ContentStore backed by a map.
type MapStore map[string][]byte

func (s MapStore) Get(digest string) ([]byte, error) {
	c, ok := s[digest]
	if !ok {
		return nil, fmt.Errorf("no content for digest %s", digest)
	}
	return c, nil
}

// Apply runs every op in the plan against exec. Each op is guarded and
// idempotent: replaying a completed plan is a no-op, and re-running a
// partially applied plan resumes safely without deleting content that no
// longer matches the planned old state.
func Apply(plan Plan, exec Executor, store ContentStore) error {
	for i, op := range plan.Ops {
		if err := applyOp(exec, store, op); err != nil {
			return fmt.Errorf("plan op %d (%s): %w", i, op, err)
		}
	}
	return nil
}

func matches(e, old Entry) bool {
	return e.Kind == old.Kind && e.Digest == old.Digest && e.Target == old.Target
}

func applyOp(exec Executor, store ContentStore, op Op) error {
	switch op.Kind {
	case OpMoveAside:
		cur, ok := exec.Stat(op.Path)
		if !ok {
			return nil // already moved aside (or never existed)
		}
		if !matches(cur, op.Old) {
			// Content at the path changed since planning; leave it alone
			// rather than moving unfamiliar data aside.
			return nil
		}
		if _, exists := exec.Stat(op.Backup); exists {
			return fmt.Errorf("backup path %q already exists", op.Backup)
		}
		return exec.Rename(op.Path, op.Backup)

	case OpRemove:
		cur, ok := exec.Stat(op.Path)
		if !ok {
			return nil
		}
		if !matches(cur, op.Old) {
			// Not the content we planned to delete; preserve it.
			return nil
		}
		return exec.Remove(op.Path)

	case OpMkdir:
		cur, ok := exec.Stat(op.Path)
		if ok {
			if cur.Kind == KindDir {
				return nil
			}
			return fmt.Errorf("path %q exists and is a %s, not a directory", op.Path, cur.Kind)
		}
		return exec.Mkdir(op.Path)

	case OpWriteFile:
		cur, ok := exec.Stat(op.Path)
		if ok {
			if cur.Kind != KindFile {
				return fmt.Errorf("path %q exists and is a %s, not a file", op.Path, cur.Kind)
			}
			if cur.Digest == op.Digest {
				return nil
			}
		}
		content, err := store.Get(op.Digest)
		if err != nil {
			return err
		}
		return exec.WriteFile(op.Path, content)

	case OpSymlink:
		cur, ok := exec.Stat(op.Path)
		if ok {
			if cur.Kind != KindSymlink {
				return fmt.Errorf("path %q exists and is a %s, not a symlink", op.Path, cur.Kind)
			}
			if cur.Target == op.Target {
				return nil
			}
			if err := exec.Remove(op.Path); err != nil {
				return err
			}
		}
		return exec.Symlink(op.Path, op.Target)

	case OpCleanupBackup:
		cur, ok := exec.Stat(op.Path)
		if !ok {
			return nil
		}
		if !matches(cur, op.Old) {
			// Backup content changed (or something else landed here);
			// never delete unfamiliar content.
			return nil
		}
		return exec.Remove(op.Path)
	}
	return fmt.Errorf("unknown op kind %d", int(op.Kind))
}
