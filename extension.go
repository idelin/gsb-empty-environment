package netsec

import "fmt"

const (
	// ExtPosixRename is the OpenSSH atomic rename extension.
	ExtPosixRename = "posix-rename@openssh.com"

	// ExtHardlink is the OpenSSH hard link extension.
	ExtHardlink = "hardlink@openssh.com"

	// ExtCopyData is the copy-data extension.
	ExtCopyData = "copy-data"

	// ExtFsetstat is the fsetstat extension.
	ExtFsetstat = "fsetstat@openssh.com"
)

// IsSupportedExtension reports whether name matches one of the exact
// extension names this package authorizes. Matching is intentionally exact:
// an @domain suffix does not make an unknown extension acceptable.
func IsSupportedExtension(name string) bool {
	switch name {
	case ExtPosixRename, ExtHardlink, ExtCopyData, ExtFsetstat:
		return true
	default:
		return false
	}
}

// ExtensionRequest is a decoded SSH_FXP_EXTENDED request. Source and Target
// are used by rename, hardlink, and copy-data. Path is used by single-path
// extensions such as fsetstat; Target is also accepted for fsetstat when Path
// is empty.
//
// copy-data and fsetstat often carry handles rather than paths on the wire.
// The server must supply the backing path it associated with each handle so
// that the same permission and confinement checks can be applied.
type ExtensionRequest struct {
	Name   string
	Source string
	Target string
	Path   string
}

// AuthorizeExtension maps an extension request onto basic permissions and
// authorizes the corresponding paths. Unknown extension names always produce
// SSH_FX_OP_UNSUPPORTED, regardless of whether they contain an @domain suffix.
func (a *Authorizer) AuthorizeExtension(request ExtensionRequest) error {
	switch request.Name {
	case ExtPosixRename:
		if request.Source == "" || request.Target == "" {
			return authError(SSH_FX_BAD_MESSAGE, request.Name, "", fmt.Errorf("%w: source and target are required", ErrBadMessage))
		}
		return a.AuthorizeRename(request.Source, request.Target)
	case ExtHardlink:
		if request.Source == "" || request.Target == "" {
			return authError(SSH_FX_BAD_MESSAGE, request.Name, "", fmt.Errorf("%w: source and target are required", ErrBadMessage))
		}
		return a.AuthorizeHardlink(request.Source, request.Target)
	case ExtCopyData:
		if request.Source == "" || request.Target == "" {
			return authError(SSH_FX_BAD_MESSAGE, request.Name, "", fmt.Errorf("%w: source and target are required", ErrBadMessage))
		}
		return a.AuthorizeCopyData(request.Source, request.Target)
	case ExtFsetstat:
		target := request.Path
		if target == "" {
			target = request.Target
		}
		if target == "" {
			return authError(SSH_FX_BAD_MESSAGE, request.Name, "", fmt.Errorf("%w: path is required", ErrBadMessage))
		}
		return a.AuthorizeFsetstat(target)
	default:
		return authError(SSH_FX_OP_UNSUPPORTED, request.Name, "", fmt.Errorf("%w: %q", ErrUnsupported, request.Name))
	}
}

// AuthorizeExtensionPaths is a convenience form for servers that decode
// extension arguments into positional paths. Rename, hardlink, and copy-data
// require two paths (source then target); fsetstat requires one.
func (a *Authorizer) AuthorizeExtensionPaths(name string, paths ...string) error {
	switch name {
	case ExtPosixRename, ExtHardlink, ExtCopyData:
		if len(paths) != 2 {
			return authError(SSH_FX_BAD_MESSAGE, name, "", fmt.Errorf("%w: expected 2 paths, got %d", ErrBadMessage, len(paths)))
		}
		return a.AuthorizeExtension(ExtensionRequest{Name: name, Source: paths[0], Target: paths[1]})
	case ExtFsetstat:
		if len(paths) != 1 {
			return authError(SSH_FX_BAD_MESSAGE, name, "", fmt.Errorf("%w: expected 1 path, got %d", ErrBadMessage, len(paths)))
		}
		return a.AuthorizeExtension(ExtensionRequest{Name: name, Path: paths[0]})
	default:
		return authError(SSH_FX_OP_UNSUPPORTED, name, "", fmt.Errorf("%w: %q", ErrUnsupported, name))
	}
}
