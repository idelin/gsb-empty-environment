package symbolcache

import (
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Key identifies a PDB exactly the way a symbol server index does: the PDB
// file name plus the GUID and age recorded in its debug directory.
type Key struct {
	// FileName is the bare PDB name, e.g. "app.pdb". It only builds the
	// on-disk and URL index path; identity is established by GUID and Age.
	FileName string
	// GUID is the 16-byte PDB GUID in raw (on-disk) byte order.
	GUID [16]byte
	// Age is the monotonically increasing PDB age.
	Age uint32
}

// index returns the canonical symbol-server relative path
// "<NAME>/<GUIDHEX><AGE>/<NAME>".
func (k Key) index() (string, error) {
	name := k.FileName
	// Reject any path structure before Base() can silently strip it away.
	if name == "" || name == "." || name == ".." ||
		filepath.Base(name) != name ||
		strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("symbolcache: invalid PDB file name %q", k.FileName)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".pdb") {
		return "", fmt.Errorf("symbolcache: PDB file name must end in .pdb: %q", k.FileName)
	}
	return path.Join(name, k.guidHex()+fmt.Sprintf("%X", k.Age), name), nil
}

func (k Key) guidHex() string {
	return strings.ToUpper(hex.EncodeToString(k.GUID[:]))
}
