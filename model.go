package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MaterialKind identifies what an exported item reveals.
type MaterialKind string

const (
	// XPub is an extended public key. It carries a chain code and can derive
	// public keys for non-hardened descendants.
	XPub MaterialKind = "xpub"
	// XPrv is an extended private key. It can derive the entire descendant
	// subtree, including hardened children.
	XPrv MaterialKind = "xpriv"
	// LeafPriv is a standalone private key at a leaf (or, conservatively, any
	// private scalar without a chain code).
	LeafPriv MaterialKind = "leaf"
)

// KeyKind is an alias for MaterialKind.
type KeyKind = MaterialKind

// Common aliases for callers that use longer or differently abbreviated names.
const (
	ExtendedPublicKey  = XPub
	ExtendedPrivateKey = XPrv
	ExtendedPubKey     = XPub
	ExtendedPrivKey    = XPrv
	ExtendedPublic     = XPub
	ExtendedPrivate    = XPrv
	LeafPrivateKey     = LeafPriv
	PrivateKey         = LeafPriv
	Xpub               = XPub
	Xprv               = XPrv
	PubKey             = XPub
	PrivKey            = LeafPriv
	KindXPub           = XPub
	KindXPrv           = XPrv
	KindLeaf           = LeafPriv
	PublicMaterial     = XPub
	PrivateMaterial    = LeafPriv
)

// NormalizeKind converts common spellings into one of XPub, XPrv, or LeafPriv.
func NormalizeKind(kind MaterialKind) (MaterialKind, error) {
	normalized := strings.ToLower(strings.TrimSpace(string(kind)))
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	switch normalized {
	case "xpub", "extendedpublickey", "extendedpubkey", "extpub", "public", "pub":
		return XPub, nil
	case "xprv", "xpriv", "extendedprivatekey", "extendedprivkey", "extpriv", "private", "priv":
		return XPrv, nil
	case "leaf", "leafprivatekey", "leafpriv", "privatekey", "privkey", "key":
		return LeafPriv, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidMaterial, kind)
	}
}

// ExportedMaterial is one item handed to an auditor. Path is accepted as a
// string for convenient JSON and struct-literal construction;
// DerivationPath can be used instead when the caller already has a parsed
// path. At least one of them must identify the key's location.
type ExportedMaterial struct {
	Kind           MaterialKind `json:"kind"`
	Type           MaterialKind `json:"type,omitempty"`
	MaterialType   MaterialKind `json:"materialType,omitempty"`
	Path           string       `json:"path,omitempty"`
	DerivationPath Path         `json:"derivationPath,omitempty"`
	Key            string       `json:"key,omitempty"`
	ChainCode      string       `json:"chainCode,omitempty"`
	Label          string       `json:"label,omitempty"`
}

// Aliases used by different wallet service layers.
type (
	Export         = ExportedMaterial
	Material       = ExportedMaterial
	ExportedKey    = ExportedMaterial
	KeyExport      = ExportedMaterial
	MaterialExport = ExportedMaterial
	ExportItem     = ExportedMaterial
)

// UnmarshalJSON accepts both kind spellings and either a string path or a
// structured derivationPath. It also accepts the common "type" spelling.
func (material *ExportedMaterial) UnmarshalJSON(data []byte) error {
	type alias ExportedMaterial
	var wire alias
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*material = ExportedMaterial(wire)
	if material.Kind == "" {
		if material.Type != "" {
			material.Kind = material.Type
		} else if material.MaterialType != "" {
			material.Kind = material.MaterialType
		}
	}
	if kind, err := NormalizeKind(material.Kind); err == nil {
		material.Kind = kind
	}
	return nil
}

// WalletNode describes one node in a deterministic wallet tree. Path may hold
// an absolute path (m/...) or, for a non-root child, a relative suffix
// (for example "0/1'"). If Path is omitted, Index and Hardened describe the
// edge from the parent. Children are visited recursively.
type WalletNode struct {
	Path           string        `json:"path,omitempty"`
	DerivationPath Path          `json:"derivationPath,omitempty"`
	Index          uint32        `json:"index,omitempty"`
	Hardened       bool          `json:"hardened,omitempty"`
	Children       []*WalletNode `json:"children,omitempty"`
	Label          string        `json:"label,omitempty"`
}

// Aliases for tree construction code.
type (
	Node     = WalletNode
	TreeNode = WalletNode
)

// WalletTree is the wallet structure under audit. Root is the normal form;
// Nodes is accepted as a flat list for importers that do not preserve nesting.
type WalletTree struct {
	Root  *WalletNode   `json:"root,omitempty"`
	Nodes []*WalletNode `json:"nodes,omitempty"`
	Paths []string      `json:"paths,omitempty"`
}

// Tree is a short alias for WalletTree.
type Tree = WalletTree

// AuditRequest is the importable input to an exposure audit.
type AuditRequest struct {
	Materials            []ExportedMaterial `json:"materials,omitempty"`
	Exports              []ExportedMaterial `json:"exports,omitempty"`
	ExportedMaterials    []ExportedMaterial `json:"exportedMaterials,omitempty"`
	Tree                 *WalletTree        `json:"tree,omitempty"`
	WalletTree           *WalletTree        `json:"walletTree,omitempty"`
	Wallet               *WalletTree        `json:"wallet,omitempty"`
	Root                 *WalletNode        `json:"root,omitempty"`
	Nodes                []*WalletNode      `json:"nodes,omitempty"`
	Baseline             *AuditReport       `json:"baseline,omitempty"`
	Previous             *AuditReport       `json:"previous,omitempty"`
	PreviousReport       *AuditReport       `json:"previousReport,omitempty"`
	Prior                *AuditReport       `json:"prior,omitempty"`
	LastReport           *AuditReport       `json:"lastReport,omitempty"`
	PreviouslyExposed    []string           `json:"previouslyExposed,omitempty"`
	PreviousExposed      []string           `json:"previousExposed,omitempty"`
	PreviousExposedPaths []string           `json:"previousExposedPaths,omitempty"`
}

// Request and AuditInput are aliases used by service adapters.
type (
	Request    = AuditRequest
	AuditInput = AuditRequest
)

// ChainStep is the structured form of one evidence or inference step. Chain
// contains the same steps in readable text.
type ChainStep struct {
	Action   string       `json:"action"`
	Path     string       `json:"path,omitempty"`
	From     string       `json:"from,omitempty"`
	To       string       `json:"to,omitempty"`
	Material MaterialKind `json:"material,omitempty"`
	Detail   string       `json:"detail,omitempty"`
}

const (
	ActionExport            = "export"
	ActionDeriveXPub        = "derive-xpub"
	ActionDeriveXPrv        = "derive-xprv"
	ActionExposeDescendant  = "expose-descendant"
	ActionRecoverXPrv       = "recover-xprv"
	ActionRecoverPrivateKey = "recover-private-key"
)

// Exposure records one private key that the auditor considers exposed.
type Exposure struct {
	Path            string      `json:"path"`
	PrivateKeyPath  string      `json:"privateKeyPath"`
	PathSegments    Path        `json:"pathSegments,omitempty"`
	Chain           []string    `json:"chain"`
	DerivationChain []string    `json:"derivationChain,omitempty"`
	Steps           []ChainStep `json:"steps,omitempty"`
	Reason          string      `json:"reason,omitempty"`
}

// AuditReport is the importable result. Exposed is sorted canonically, and
// ExposedPaths mirrors its Path values for callers that only need a set.
type AuditReport struct {
	Safe               bool                `json:"safe"`
	Exposed            []Exposure          `json:"exposed"`
	ExposedPaths       []string            `json:"exposedPaths"`
	Paths              []string            `json:"paths,omitempty"`
	ExposedPrivateKeys []string            `json:"exposedPrivateKeys,omitempty"`
	PrivateKeyPaths    []string            `json:"privateKeyPaths,omitempty"`
	Chains             map[string][]string `json:"chains,omitempty"`
	ByPath             map[string]Exposure `json:"byPath,omitempty"`
	Materials          []ExportedMaterial  `json:"materials,omitempty"`
	Tree               *WalletTree         `json:"tree,omitempty"`
	Request            *AuditRequest       `json:"request,omitempty"`
}

// Result, Report, AuditResult, and ExposureReport are aliases for AuditReport.
type (
	Report         = AuditReport
	Result         = AuditReport
	AuditResult    = AuditReport
	ExposureReport = AuditReport
	AuditOutput    = AuditReport
)

// Errors returned by the package. Use errors.Is to test for them.
var (
	ErrInvalidPath           = errors.New("invalid derivation path")
	ErrInvalidMaterial       = errors.New("invalid exported material")
	ErrUnknownPath           = errors.New("material path is absent from wallet tree")
	ErrDuplicatePath         = errors.New("duplicate wallet tree path")
	ErrInvalidTree           = errors.New("invalid wallet tree")
	ErrNoBaseline            = errors.New("incremental export requires a baseline audit")
	ErrPreviouslySafeExposed = errors.New("new export exposes a previously safe private key")
)

// Additional error aliases used by exporters.
var (
	ErrNewExposure        = ErrPreviouslySafeExposed
	ErrUnsafeExport       = ErrPreviouslySafeExposed
	ErrExportRejected     = ErrPreviouslySafeExposed
	ErrExposureRegression = ErrPreviouslySafeExposed
)

// UnsafeExportError rejects a whole incremental request atomically.
type UnsafeExportError struct {
	NewPaths          []string           `json:"newPaths"`
	NewlyExposedPaths []string           `json:"newlyExposedPaths,omitempty"`
	ExposedPaths      []string           `json:"exposedPaths,omitempty"`
	Exposed           []Exposure         `json:"exposed,omitempty"`
	AddedMaterials    []ExportedMaterial `json:"addedMaterials,omitempty"`
	Report            *AuditReport       `json:"report,omitempty"`
}

func (e *UnsafeExportError) Error() string {
	if len(e.NewPaths) == 0 {
		return ErrPreviouslySafeExposed.Error()
	}
	return fmt.Sprintf("%s: %s", ErrPreviouslySafeExposed, strings.Join(e.NewPaths, ", "))
}

// Is lets errors.Is match the package sentinel even through wrapping.
func (e *UnsafeExportError) Is(target error) bool {
	return target == ErrPreviouslySafeExposed
}

// Unwrap exposes the sentinel to errors.Unwrap.
func (e *UnsafeExportError) Unwrap() error {
	return ErrPreviouslySafeExposed
}

func newUnsafeExportError(newPaths []string, candidate AuditReport, additions []ExportedMaterial) *UnsafeExportError {
	return &UnsafeExportError{
		NewPaths:          append([]string(nil), newPaths...),
		NewlyExposedPaths: append([]string(nil), newPaths...),
		ExposedPaths:      append([]string(nil), newPaths...),
		Exposed:           append([]Exposure(nil), candidate.Exposed...),
		AddedMaterials:    cloneMaterials(additions),
		Report:            &candidate,
	}
}

// NewExposureError is a compatibility alias for UnsafeExportError.
type NewExposureError = UnsafeExportError
type NewlyExposedError = UnsafeExportError
