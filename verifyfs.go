package netsec

import (
	"fmt"
	"io/fs"
)

// VerifyFS reads the podspec at relPath from fsys and runs the full
// verification pipeline. A mirror service can expose its on-disk or embedded
// specs repository through os.DirFS (or any fs.FS) and pass the request path
// straight in.
func (v *Verifier) VerifyFS(fsys fs.FS, repoKey, relPath string) (*Podspec, error) {
	data, err := fs.ReadFile(fsys, relPath)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read %s: %v", ErrInvalidPodspec, relPath, err)
	}
	return v.Verify(repoKey, relPath, data)
}
