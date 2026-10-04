package carc

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxSymlinkDepth bounds symlink resolution to break link loops.
const maxSymlinkDepth = 40

// Extract unpacks the archive at archivePath into destDir.
//
// destDir must not exist yet. Extraction happens in a temporary sibling
// directory which is renamed into place on success and removed on any
// failure, so a failed run leaves no partial output behind.
//
// Security rules enforced for every entry:
//   - names must be relative and stay inside the root (no absolute
//     paths, no ".." escapes);
//   - a path may not be claimed twice with conflicting types, and
//     non-directory entries may not be duplicated at all;
//   - parent directories are resolved component by component, following
//     symlinks (including ones created by earlier archive entries) while
//     guaranteeing the result stays inside the root, so archive symlinks
//     cannot redirect later files outside;
//   - hardlinks may only target regular files already extracted from
//     this same archive (verified internal objects).
func Extract(archivePath, destDir string) error {
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(absDest); err == nil {
		return fmt.Errorf("carc: destination %q already exists", destDir)
	} else if !os.IsNotExist(err) {
		return err
	}

	parent := filepath.Dir(absDest)
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		parent = resolved
	}
	tmp, err := ioutil.TempDir(parent, "."+filepath.Base(absDest)+".tmp-")
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmp)
		}
	}()

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	rd, err := NewReader(f)
	if err != nil {
		return err
	}

	x := &extractor{
		root:  tmp,
		seen:  make(map[string]byte),
		files: make(map[string]bool),
	}
	for {
		h, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := x.apply(h, rd); err != nil {
			return err
		}
	}

	if err := os.Rename(tmp, absDest); err != nil {
		return err
	}
	success = true
	return nil
}

type extractor struct {
	root  string          // absolute, symlink-free extraction root
	seen  map[string]byte // cleaned name -> entry type
	files map[string]bool // cleaned names of extracted regular files
}

func (x *extractor) apply(h *Header, rd *Reader) error {
	rel, err := cleanName(h.Name)
	if err != nil {
		return err
	}
	if prev, ok := x.seen[rel]; ok {
		if prev != h.Type {
			return fmt.Errorf("carc: entry %q: duplicate target with conflicting types (%c then %c)", rel, prev, h.Type)
		}
		if h.Type != TypeDir {
			return fmt.Errorf("carc: entry %q: duplicate target", rel)
		}
	}

	switch h.Type {
	case TypeDir:
		err = x.applyDir(rel)
	case TypeFile:
		err = x.applyFile(rel, h.Size, rd)
	case TypeSymlink:
		err = x.applySymlink(rel, h.Linkname)
	case TypeHardlink:
		err = x.applyHardlink(rel, h.Linkname)
	default:
		err = fmt.Errorf("carc: entry %q: unknown type %q", rel, h.Type)
	}
	if err != nil {
		return err
	}

	x.seen[rel] = h.Type
	if h.Type == TypeFile {
		x.files[rel] = true
	}
	return nil
}

// cleanName validates an archive-supplied name and returns its cleaned
// slash-separated relative form.
func cleanName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("carc: empty entry name")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("carc: entry %q: name contains NUL", name)
	}
	if strings.HasPrefix(name, "/") || path.IsAbs(name) {
		return "", fmt.Errorf("carc: entry %q: absolute paths are not allowed", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("carc: entry %q: path escapes extraction root", name)
	}
	return cleaned, nil
}

func (x *extractor) applyDir(rel string) error {
	abs, err := x.resolve(rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0755)
}

func (x *extractor) applyFile(rel string, size int64, rd *Reader) error {
	final, err := x.resolveParent(rel)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(final, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(f, rd, size)
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("carc: entry %q: %v", rel, copyErr)
	}
	return closeErr
}

func (x *extractor) applySymlink(rel, target string) error {
	final, err := x.resolveParent(rel)
	if err != nil {
		return err
	}
	// The target itself is stored verbatim (tar semantics); safety is
	// enforced when later entries traverse through this link.
	return os.Symlink(target, final)
}

func (x *extractor) applyHardlink(rel, target string) error {
	targetRel, err := cleanName(target)
	if err != nil {
		return fmt.Errorf("carc: entry %q: invalid hardlink target: %v", rel, err)
	}
	if !x.files[targetRel] {
		return fmt.Errorf("carc: entry %q: hardlink target %q is not a previously extracted file", rel, targetRel)
	}
	targetAbs, err := x.resolve(targetRel)
	if err != nil {
		return err
	}
	final, err := x.resolveParent(rel)
	if err != nil {
		return err
	}
	return os.Link(targetAbs, final)
}

// resolveParent resolves rel's parent directory (following symlinks,
// confined to the root), creates it if needed, and returns the absolute
// path of rel itself.
func (x *extractor) resolveParent(rel string) (string, error) {
	dir, base := path.Split(rel)
	absDir, err := x.resolve(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(absDir, base), nil
}

func (x *extractor) resolve(rel string) (string, error) {
	return resolveWithinRoot(x.root, rel, 0)
}

// resolveWithinRoot resolves every existing symlink component of the
// slash-separated relative path rel under root, returning an absolute
// path guaranteed to be inside root. Trailing components that do not
// exist yet are kept verbatim. depth guards against symlink loops.
func resolveWithinRoot(root, rel string, depth int) (string, error) {
	if depth > maxSymlinkDepth {
		return "", fmt.Errorf("carc: %q: too many levels of symbolic links", rel)
	}
	cur := root
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		next := filepath.Join(cur, part)
		fi, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				// Nothing exists from here on; append the rest verbatim.
				for _, rest := range parts[i+1:] {
					if rest == "" || rest == "." {
						continue
					}
					next = filepath.Join(next, rest)
				}
				return next, nil
			}
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		var combined string
		if filepath.IsAbs(target) {
			combined = filepath.Clean(target)
		} else {
			combined = filepath.Join(cur, target)
		}
		sub, err := relUnderRoot(root, combined)
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		sb.WriteString(sub)
		for _, rest := range parts[i+1:] {
			if rest == "" || rest == "." {
				continue
			}
			sb.WriteString("/")
			sb.WriteString(rest)
		}
		return resolveWithinRoot(root, sb.String(), depth+1)
	}
	return cur, nil
}

// relUnderRoot converts abs to a slash-separated path relative to root,
// rejecting anything outside. The separator-anchored prefix check keeps
// same-prefix siblings (e.g. "out-evil" next to "out") outside.
func relUnderRoot(root, abs string) (string, error) {
	root = filepath.Clean(root)
	abs = filepath.Clean(abs)
	if abs == root {
		return ".", nil
	}
	prefix := root + string(os.PathSeparator)
	if !strings.HasPrefix(abs, prefix) {
		return "", fmt.Errorf("carc: path %q escapes extraction root", abs)
	}
	return filepath.ToSlash(abs[len(prefix):]), nil
}
