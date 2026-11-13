package netsec

// NewWalletTree builds a wallet tree from a flat list of absolute path
// strings. Missing intermediate nodes are created by the auditor.
func NewWalletTree(paths ...string) *WalletTree {
	return &WalletTree{Paths: append([]string(nil), paths...)}
}

// NewTree is an alias for NewWalletTree.
func NewTree(paths ...string) *WalletTree {
	return NewWalletTree(paths...)
}

// NewWalletNode builds one node. Its children may use absolute or relative
// paths.
func NewWalletNode(path string, children ...*WalletNode) *WalletNode {
	return &WalletNode{Path: path, Children: children}
}

// NewNode is an alias for NewWalletNode.
func NewNode(path string, children ...*WalletNode) *WalletNode {
	return NewWalletNode(path, children...)
}

// NewExportedMaterial is a convenience constructor for an audit item.
func NewExportedMaterial(kind MaterialKind, path, key string) ExportedMaterial {
	return ExportedMaterial{Kind: kind, Path: path, Key: key}
}

// NewExport is an alias for NewExportedMaterial.
func NewExport(kind MaterialKind, path, key string) ExportedMaterial {
	return NewExportedMaterial(kind, path, key)
}

// AuditWallet is an alias for Audit.
func AuditWallet(args ...any) (AuditReport, error) {
	return Audit(args...)
}

// BuildExposureReport is an alias for Audit.
func BuildExposureReport(args ...any) (AuditReport, error) {
	return Audit(args...)
}

// CheckExposure is an alias for Audit.
func CheckExposure(args ...any) (AuditReport, error) {
	return Audit(args...)
}
