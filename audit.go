package netsec

import (
	"fmt"
	"sort"
	"strings"
)

type provenance struct {
	path  Path
	steps []ChainStep
}

func directProvenance(path Path, kind MaterialKind, detail string) *provenance {
	return &provenance{
		path: clonePath(path),
		steps: []ChainStep{{
			Action:   ActionExport,
			Path:     path.String(),
			Material: kind,
			Detail:   detail,
		}},
	}
}

func extendProvenance(parent *provenance, step ChainStep) *provenance {
	steps := make([]ChainStep, 0, len(parent.steps)+1)
	steps = append(steps, parent.steps...)
	steps = append(steps, step)
	return &provenance{path: clonePath(parent.path), steps: steps}
}

func chainText(steps []ChainStep) []string {
	text := make([]string, len(steps))
	for i, step := range steps {
		text[i] = renderStep(step)
	}
	return text
}

func renderStep(step ChainStep) string {
	switch step.Action {
	case ActionExport:
		return fmt.Sprintf("export %s %s", kindText(step.Material), step.Path)
	case ActionDeriveXPub:
		return fmt.Sprintf("derive xpub %s from xpub %s", step.To, step.From)
	case ActionDeriveXPrv:
		if step.Detail != "" {
			return fmt.Sprintf("derive xprv %s from xprv %s (%s)", step.To, step.From, step.Detail)
		}
		return fmt.Sprintf("derive xprv %s from xprv %s", step.To, step.From)
	case ActionExposeDescendant:
		return fmt.Sprintf("expose priv %s as descendant of priv %s", step.To, step.From)
	case ActionRecoverXPrv:
		return fmt.Sprintf("recover xprv %s from xpub %s and priv %s", step.Path, step.From, step.To)
	case ActionRecoverPrivateKey:
		return fmt.Sprintf("recover priv %s from xpub %s and priv %s", step.Path, step.From, step.To)
	default:
		return step.Detail
	}
}

func kindText(kind MaterialKind) string {
	switch kind {
	case XPub:
		return "xpub"
	case XPrv:
		return "xprv"
	case LeafPriv:
		return "priv"
	default:
		return string(kind)
	}
}

type closure struct {
	tree        *treeIndex
	xpub        map[string]*provenance
	xpriv       map[string]*provenance
	private     map[string]*provenance
	authorities map[string]*provenance
}

func newClosure(tree *treeIndex) *closure {
	return &closure{
		tree:        tree,
		xpub:        map[string]*provenance{},
		xpriv:       map[string]*provenance{},
		private:     map[string]*provenance{},
		authorities: map[string]*provenance{},
	}
}

func (c *closure) seed(materials []ExportedMaterial) {
	for _, material := range materials {
		path := material.DerivationPath
		key := path.String()

		switch material.Kind {
		case XPub:
			if c.xpub[key] == nil {
				c.xpub[key] = directProvenance(path, XPub, "extended public key with chain code")
			}
		case XPrv:
			if c.xpriv[key] == nil {
				c.xpriv[key] = directProvenance(path, XPrv, "extended private key")
			}
			if c.private[key] == nil {
				c.private[key] = c.xpriv[key]
			}
			if c.authorities[key] == nil {
				c.authorities[key] = c.xpriv[key]
			}
		case LeafPriv:
			if c.private[key] == nil {
				c.private[key] = directProvenance(path, LeafPriv, "standalone private key")
			}
			if c.authorities[key] == nil {
				c.authorities[key] = c.private[key]
			}
		}
	}
}

func (c *closure) nodeKeys() []string {
	keys := make([]string, 0, len(c.tree.nodes))
	for key := range c.tree.nodes {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return ComparePaths(c.tree.nodes[keys[i]].path, c.tree.nodes[keys[j]].path) < 0
	})
	return keys
}

func (c *closure) run() {
	for {
		changed := false

		// An exported or derived xpub gives the xpub of every non-hardened
		// descendant. This is the public derivation closure.
		for _, key := range c.nodeKeys() {
			node := c.tree.nodes[key]
			xpubCause := c.xpub[key]
			if xpubCause == nil {
				continue
			}
			for _, child := range node.children {
				if child.path[len(child.path)-1].Hardened || c.xpub[child.key] != nil {
					continue
				}
				c.xpub[child.key] = extendProvenance(xpubCause, ChainStep{
					Action: ActionDeriveXPub,
					From:   node.path.String(),
					To:     child.path.String(),
					Detail: "non-hardened child",
				})
				changed = true
			}
		}

		// An xprv derives every descendant, including hardened children.
		for _, key := range c.nodeKeys() {
			node := c.tree.nodes[key]
			xprivCause := c.xpriv[key]
			if xprivCause == nil {
				continue
			}
			for _, child := range node.children {
				if c.xpriv[child.key] == nil {
					detail := "non-hardened child"
					if child.path[len(child.path)-1].Hardened {
						detail = "hardened child"
					}
					c.xpriv[child.key] = extendProvenance(xprivCause, ChainStep{
						Action: ActionDeriveXPrv,
						From:   node.path.String(),
						To:     child.path.String(),
						Detail: detail,
					})
					changed = true
				}
				if c.private[child.key] == nil {
					c.private[child.key] = c.xpriv[child.key]
					changed = true
				}
			}
		}

		// The conservative subtree rule for any directly exported private
		// material (including a standalone leaf key).
		for _, key := range c.nodeKeys() {
			node := c.tree.nodes[key]
			authority := c.authorities[key]
			if authority == nil {
				continue
			}
			for _, child := range node.children {
				cause := extendProvenance(authority, ChainStep{
					Action: ActionExposeDescendant,
					From:   node.path.String(),
					To:     child.path.String(),
					Detail: "descendant of exported private material",
				})
				if c.private[child.key] == nil {
					c.private[child.key] = cause
					changed = true
				}
				if c.authorities[child.key] == nil {
					c.authorities[child.key] = cause
					changed = true
				}
			}
		}

		// Reverse derivation: xpub(parent) + private scalar of any
		// non-hardened child recovers the parent xprv. The recovered private
		// material is then fed into the rules above on the next iteration,
		// which is what makes leakage climb level by level.
		for _, key := range c.nodeKeys() {
			node := c.tree.nodes[key]
			xpubCause := c.xpub[key]
			if xpubCause == nil {
				continue
			}
			for _, child := range node.children {
				if child.path[len(child.path)-1].Hardened {
					continue
				}
				privateCause := c.private[child.key]
				if privateCause == nil || c.xpriv[key] != nil {
					continue
				}
				recovery := &provenance{
					path: clonePath(node.path),
					steps: combineSteps(xpubCause.steps, privateCause.steps, []ChainStep{{
						Action: ActionRecoverXPrv,
						Path:   node.path.String(),
						From:   node.path.String(),
						To:     child.path.String(),
						Detail: "parent xpub plus non-hardened child private key",
					}}),
				}
				c.xpriv[key] = recovery
				if c.private[key] == nil {
					c.private[key] = recovery
				}
				if c.authorities[key] == nil {
					c.authorities[key] = recovery
				}
				changed = true
			}
		}

		if !changed {
			break
		}
	}
}

func cloneSteps(steps []ChainStep) []ChainStep {
	clone := make([]ChainStep, len(steps))
	copy(clone, steps)
	return clone
}

func combineSteps(groups ...[]ChainStep) []ChainStep {
	seen := make(map[ChainStep]struct{})
	combined := make([]ChainStep, 0)
	for _, group := range groups {
		for _, step := range group {
			if _, ok := seen[step]; ok {
				continue
			}
			seen[step] = struct{}{}
			combined = append(combined, step)
		}
	}
	return combined
}

func buildReport(req AuditRequest) (AuditReport, error) {
	normalized, err := normalizeRequest(req)
	if err != nil {
		return AuditReport{}, err
	}
	tree, err := buildTreeIndex(normalized)
	if err != nil {
		return AuditReport{}, err
	}
	if err := checkMaterialPaths(tree, normalized.Materials); err != nil {
		return AuditReport{}, err
	}

	closed := newClosure(tree)
	closed.seed(normalized.Materials)
	closed.run()

	nodes := closed.nodeKeys()
	exposed := make([]Exposure, 0, len(closed.private))
	for _, key := range nodes {
		cause := closed.private[key]
		if cause == nil {
			continue
		}
		node := tree.nodes[key]
		chain := chainText(cause.steps)
		exposed = append(exposed, Exposure{
			Path:            node.path.String(),
			PrivateKeyPath:  node.path.String(),
			PathSegments:    clonePath(node.path),
			Chain:           chain,
			DerivationChain: append([]string(nil), chain...),
			Steps:           cloneSteps(cause.steps),
			Reason:          strings.Join(chain, " -> "),
		})
	}
	sort.Slice(exposed, func(i, j int) bool {
		return ComparePaths(exposed[i].PathSegments, exposed[j].PathSegments) < 0
	})

	paths := make([]string, len(exposed))
	chains := make(map[string][]string, len(exposed))
	byPath := make(map[string]Exposure, len(exposed))
	for i, exposure := range exposed {
		paths[i] = exposure.Path
		chains[exposure.Path] = append([]string(nil), exposure.Chain...)
		byPath[exposure.Path] = exposure
	}

	report := AuditReport{
		Safe:               len(exposed) == 0,
		Exposed:            exposed,
		ExposedPaths:       paths,
		Paths:              append([]string(nil), paths...),
		ExposedPrivateKeys: append([]string(nil), paths...),
		PrivateKeyPaths:    append([]string(nil), paths...),
		Chains:             chains,
		ByPath:             byPath,
		Materials:          cloneMaterials(normalized.Materials),
		Tree:               treeSnapshot(tree.root),
	}
	request := normalized
	request.Tree = report.Tree
	request.Root = nil
	request.Nodes = nil
	report.Request = &request
	return report, nil
}

func cloneMaterials(materials []ExportedMaterial) []ExportedMaterial {
	clone := make([]ExportedMaterial, len(materials))
	copy(clone, materials)
	for i := range clone {
		clone[i].DerivationPath = clonePath(clone[i].DerivationPath)
	}
	return clone
}

// Audit evaluates an importable audit request. It accepts an AuditRequest
// (value or pointer), or a material list and WalletTree as two arguments.
// The variadic form keeps older service adapters source-compatible.
func Audit(args ...any) (AuditReport, error) {
	req, err := requestFromArgs(args...)
	if err != nil {
		return AuditReport{}, err
	}
	if baseline := req.BaselineReport(); baseline != nil {
		added := AuditRequest{
			Materials: append([]ExportedMaterial(nil), req.Materials...),
			Tree:      req.Tree,
		}
		return applyToReport(*baseline, added)
	}
	if previous := req.BaselineExposed(); len(previous) > 0 {
		candidateInput := req
		candidateInput.PreviouslyExposed = nil
		candidateInput.PreviousExposed = nil
		candidate, err := buildReport(candidateInput)
		if err != nil {
			return AuditReport{}, err
		}
		if newPaths := newlyExposed(previous, candidate.ExposedPaths); len(newPaths) > 0 {
			unsafe := newUnsafeExportError(newPaths, candidate, nil)
			return candidate, unsafe
		}
		return candidate, nil
	}
	return buildReport(req)
}

// Review is an alias for Audit.
func Review(args ...any) (AuditReport, error) {
	return Audit(args...)
}

// Analyze is an alias for Audit.
func Analyze(args ...any) (AuditReport, error) {
	return Audit(args...)
}

// AuditExports is an alias for Audit.
func AuditExports(args ...any) (AuditReport, error) {
	return Audit(args...)
}

// ReviewExports is an alias for Audit.
func ReviewExports(args ...any) (AuditReport, error) {
	return Audit(args...)
}

// Evaluate is an alias for Audit.
func Evaluate(args ...any) (AuditReport, error) {
	return Audit(args...)
}

// Audit is a small compatibility helper for callers that prefer a
// one-argument request method.
func (request AuditRequest) Audit() (AuditReport, error) {
	return Audit(request)
}
