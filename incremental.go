package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// AuditSession is a transactional exporter. Accepted material is retained;
// a candidate batch that reveals even one previously safe path is rejected
// without changing the retained state.
type AuditSession struct {
	mu        sync.Mutex
	request   AuditRequest
	report    AuditReport
	treeFixed bool
}

// Session, State, and Exporter are aliases for AuditSession.
type (
	Session       = AuditSession
	AuditState    = AuditSession
	Exporter      = AuditSession
	ExportSession = AuditSession
)

// NewAuditSession seeds an accepted baseline and audits it. Additional
// variadic forms are accepted for adapters (materials plus tree, for example).
func NewAuditSession(args ...any) (*AuditSession, error) {
	request, err := requestFromArgs(args...)
	if err != nil {
		return nil, err
	}
	report, err := Audit(request)
	if err != nil {
		return nil, err
	}
	accepted := requestFromReport(report)
	return &AuditSession{
		request: accepted,
		report:  report,
		treeFixed: request.Tree != nil || request.Root != nil || len(request.Nodes) > 0 ||
			request.BaselineReport() != nil || len(request.BaselineExposed()) > 0,
	}, nil
}

// NewSession is an alias for NewAuditSession.
func NewSession(args ...any) (*AuditSession, error) {
	return NewAuditSession(args...)
}

// NewExporter is an alias for NewAuditSession.
func NewExporter(args ...any) (*AuditSession, error) {
	return NewAuditSession(args...)
}

// Apply checks candidate materials against the accepted state and commits the
// whole batch only when no newly exposed path appears.
func (session *AuditSession) Apply(args ...any) (AuditReport, error) {
	if session == nil {
		return AuditReport{}, fmt.Errorf("%w: nil session", ErrNoBaseline)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.applyLocked(args...)
}

// Add is an alias for Apply.
func (session *AuditSession) Add(args ...any) (AuditReport, error) {
	return session.Apply(args...)
}

// Export is an alias for Apply.
func (session *AuditSession) Export(args ...any) (AuditReport, error) {
	return session.Apply(args...)
}

// Approve is an alias for Apply.
func (session *AuditSession) Approve(args ...any) (AuditReport, error) {
	return session.Apply(args...)
}

// Accept is an alias for Apply.
func (session *AuditSession) Accept(args ...any) (AuditReport, error) {
	return session.Apply(args...)
}

// AcceptExport is an alias for Apply.
func (session *AuditSession) AcceptExport(args ...any) (AuditReport, error) {
	return session.Apply(args...)
}

// AddExport is an alias for Apply.
func (session *AuditSession) AddExport(args ...any) (AuditReport, error) {
	return session.Apply(args...)
}

// Commit is an alias for Apply.
func (session *AuditSession) Commit(args ...any) (AuditReport, error) {
	return session.Apply(args...)
}

// ApplyMaterials is the typed convenience form of Apply.
func (session *AuditSession) ApplyMaterials(materials ...ExportedMaterial) (AuditReport, error) {
	return session.Apply(materials)
}

// DryRun evaluates a candidate batch without committing it.
func (session *AuditSession) DryRun(args ...any) (AuditReport, error) {
	if session == nil {
		return AuditReport{}, fmt.Errorf("%w: nil session", ErrNoBaseline)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	report, err := session.reviewLocked(args...)
	if unsafe, ok := err.(*UnsafeExportError); ok && unsafe.Report != nil {
		return cloneReport(*unsafe.Report), err
	}
	return report, err
}

// Preview is an alias for DryRun.
func (session *AuditSession) Preview(args ...any) (AuditReport, error) {
	return session.DryRun(args...)
}

func (session *AuditSession) applyLocked(args ...any) (AuditReport, error) {
	added, err := requestFromArgs(args...)
	if err != nil {
		return cloneReport(session.report), err
	}
	candidate, err := session.evaluateLocked(added)
	if err != nil {
		return cloneReport(session.report), err
	}
	if added.Tree != nil {
		session.treeFixed = true
	}
	session.request = requestFromReport(candidate)
	session.report = candidate
	return cloneReport(candidate), nil
}

func (session *AuditSession) reviewLocked(args ...any) (AuditReport, error) {
	added, err := requestFromArgs(args...)
	if err != nil {
		return AuditReport{}, err
	}
	return session.evaluateLocked(added)
}

func (session *AuditSession) evaluateLocked(added AuditRequest) (AuditReport, error) {
	if added.Tree != nil {
		// The tree is part of the accepted policy snapshot. A candidate export
		// cannot silently change what the auditor believes the wallet contains.
		if session.treeFixed && !sameTree(session.request.Tree, added.Tree) {
			return AuditReport{}, fmt.Errorf("%w: candidate changed wallet tree", ErrInvalidTree)
		}
	}
	if len(added.BaselineExposed()) > 0 || added.BaselineReport() != nil {
		return AuditReport{}, fmt.Errorf("%w: nested baseline in candidate export", ErrInvalidMaterial)
	}
	baseRequest := session.request
	if !session.treeFixed && added.Tree != nil {
		baseRequest.Tree = nil
	}
	return applyIncrement(session.report, baseRequest, added.Materials, added.Tree)
}

// Report returns a snapshot of the currently accepted audit.
func (session *AuditSession) Report() AuditReport {
	if session == nil {
		return AuditReport{}
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return cloneReport(session.report)
}

// Materials returns the accepted material list.
func (session *AuditSession) Materials() []ExportedMaterial {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return cloneMaterials(session.request.Materials)
}

// ExposedPaths returns the accepted exposed-path set.
func (session *AuditSession) ExposedPaths() []string {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]string(nil), session.report.ExposedPaths...)
}

// ApplyExports applies an incremental batch to a baseline report, request, or
// material list. On rejection it returns the unchanged baseline report and an
// *UnsafeExportError.
func ApplyExports(args ...any) (AuditReport, error) {
	if len(args) == 0 {
		return AuditReport{}, fmt.Errorf("%w: missing baseline", ErrNoBaseline)
	}

	switch baseline := args[0].(type) {
	case *AuditSession:
		return baseline.Apply(args[1:]...)
	case AuditSession:
		return baseline.Apply(args[1:]...)
	}

	added, err := requestFromArgs(args[1:]...)
	if err != nil {
		return AuditReport{}, err
	}

	switch baseline := args[0].(type) {
	case nil:
		candidate, err := buildReport(AuditRequest{
			Materials: append([]ExportedMaterial(nil), added.Materials...),
			Tree:      added.Tree,
		})
		if err != nil {
			return AuditReport{}, err
		}
		if newPaths := newlyExposed(nil, candidate.ExposedPaths); len(newPaths) > 0 {
			unsafe := newUnsafeExportError(newPaths, candidate, added.Materials)
			return AuditReport{}, unsafe
		}
		return candidate, nil
	case AuditReport:
		return applyToReport(baseline, added)
	case *AuditReport:
		if baseline == nil {
			return AuditReport{}, fmt.Errorf("%w: nil baseline report", ErrNoBaseline)
		}
		return applyToReport(*baseline, added)
	case AuditRequest:
		return applyToRequest(baseline, added)
	case *AuditRequest:
		if baseline == nil {
			return AuditReport{}, fmt.Errorf("%w: nil baseline request", ErrNoBaseline)
		}
		return applyToRequest(*baseline, added)
	case []ExportedMaterial:
		baseRequest := AuditRequest{
			Materials: append([]ExportedMaterial(nil), baseline...),
			Tree:      added.Tree,
		}
		return applyToRequest(baseRequest, added)
	case []string:
		candidate, err := buildReport(AuditRequest{
			Materials: append([]ExportedMaterial(nil), added.Materials...),
			Tree:      added.Tree,
		})
		if err != nil {
			return AuditReport{}, err
		}
		if newPaths := newlyExposed(baseline, candidate.ExposedPaths); len(newPaths) > 0 {
			unsafe := newUnsafeExportError(newPaths, candidate, added.Materials)
			return AuditReport{}, unsafe
		}
		return candidate, nil
	default:
		return AuditReport{}, fmt.Errorf("%w: unsupported baseline %T", ErrNoBaseline, args[0])
	}
}

// ApplyExport is an alias for ApplyExports.
func ApplyExport(args ...any) (AuditReport, error) {
	return ApplyExports(args...)
}

// AddExports is an alias for ApplyExports.
func AddExports(args ...any) (AuditReport, error) {
	return ApplyExports(args...)
}

// AcceptExports is an alias for ApplyExports.
func AcceptExports(args ...any) (AuditReport, error) {
	return ApplyExports(args...)
}

// ReviewExport validates a batch transactionally. It does not mutate any
// session supplied as the baseline.
func ReviewExport(args ...any) (AuditReport, error) {
	if len(args) > 0 {
		if session, ok := args[0].(*AuditSession); ok {
			return session.DryRun(args[1:]...)
		}
	}
	report, err := ApplyExports(args...)
	if unsafe, ok := err.(*UnsafeExportError); ok && unsafe.Report != nil {
		return *unsafe.Report, err
	}
	return report, err
}

func applyToReport(base AuditReport, added AuditRequest) (AuditReport, error) {
	baseRequest, err := requestFromAcceptedReport(base)
	if err != nil {
		return base, err
	}
	return applyIncrement(base, baseRequest, added.Materials, added.Tree)
}

func applyToRequest(base AuditRequest, added AuditRequest) (AuditReport, error) {
	baseReport, err := buildReport(base)
	if err != nil {
		return AuditReport{}, err
	}
	return applyIncrement(baseReport, requestFromReport(baseReport), added.Materials, added.Tree)
}

func applyIncrement(baseReport AuditReport, baseRequest AuditRequest, additions []ExportedMaterial, treeOverride *WalletTree) (AuditReport, error) {
	if len(baseReport.ExposedPaths) == 0 && len(baseReport.Exposed) > 0 {
		baseReport.ExposedPaths = make([]string, len(baseReport.Exposed))
		for i, exposure := range baseReport.Exposed {
			baseReport.ExposedPaths[i] = exposure.Path
		}
	}
	if treeOverride != nil && baseRequest.Tree != nil && !sameTree(baseRequest.Tree, treeOverride) {
		if !treeIsSubset(baseRequest.Tree, treeOverride) {
			return baseReport, fmt.Errorf("%w: incremental export changed wallet tree", ErrInvalidTree)
		}
	}
	candidateRequest := AuditRequest{
		Materials: append(cloneMaterials(baseRequest.Materials), cloneMaterials(additions)...),
		Tree:      baseRequest.Tree,
	}
	if treeOverride != nil {
		candidateRequest.Tree = treeOverride
	}

	candidate, err := buildReport(candidateRequest)
	if errors.Is(err, ErrUnknownPath) && treeOverride == nil && baseRequest.Tree != nil {
		// Exporters sometimes retain only the baseline subset of the tree.
		// Preserve that structure and add the paths named by the new
		// material instead of rejecting an otherwise well-formed batch.
		candidateRequest.Tree = expandTree(baseRequest.Tree, additions)
		candidate, err = buildReport(candidateRequest)
	}
	if err != nil {
		return baseReport, err
	}
	newPaths := newlyExposed(baseReport.ExposedPaths, candidate.ExposedPaths)
	if len(newPaths) > 0 {
		unsafe := newUnsafeExportError(newPaths, candidate, additions)
		return cloneReport(baseReport), unsafe
	}
	return candidate, nil
}

func expandTree(base *WalletTree, additions []ExportedMaterial) *WalletTree {
	paths := make([]string, 0)
	if base != nil && base.Root != nil {
		collectNodePaths(base.Root, &paths)
	} else if base != nil {
		paths = append(paths, base.Paths...)
		for _, node := range base.Nodes {
			if node == nil {
				continue
			}
			if node.DerivationPath != nil {
				paths = append(paths, node.DerivationPath.String())
			} else if node.Path != "" {
				paths = append(paths, node.Path)
			}
		}
	}
	for _, material := range additions {
		normalized, err := normalizeMaterial(material)
		if err != nil {
			continue
		}
		paths = append(paths, normalized.DerivationPath.String())
	}
	return &WalletTree{Paths: paths}
}

func collectNodePaths(node *WalletNode, paths *[]string) {
	if node == nil {
		return
	}
	if node.Path != "" {
		*paths = append(*paths, node.Path)
	} else if node.DerivationPath != nil {
		*paths = append(*paths, node.DerivationPath.String())
	}
	for _, child := range node.Children {
		collectNodePaths(child, paths)
	}
}

func requestFromReport(report AuditReport) AuditRequest {
	request := AuditRequest{
		Materials: cloneMaterials(report.Materials),
		Tree:      report.Tree,
	}
	if request.Tree == nil && report.Request != nil {
		request.Tree = report.Request.Tree
	}
	if len(request.Materials) == 0 && report.Request != nil {
		request.Materials = cloneMaterials(report.Request.Materials)
	}
	return request
}

func requestFromAcceptedReport(report AuditReport) (AuditRequest, error) {
	return requestFromReport(report), nil
}

func cloneReport(report AuditReport) AuditReport {
	clone := report
	clone.Exposed = append([]Exposure(nil), report.Exposed...)
	for i := range clone.Exposed {
		clone.Exposed[i].PathSegments = clonePath(clone.Exposed[i].PathSegments)
		clone.Exposed[i].Chain = append([]string(nil), clone.Exposed[i].Chain...)
		clone.Exposed[i].DerivationChain = append([]string(nil), clone.Exposed[i].DerivationChain...)
		clone.Exposed[i].Steps = cloneSteps(clone.Exposed[i].Steps)
	}
	clone.ExposedPaths = append([]string(nil), report.ExposedPaths...)
	clone.Paths = append([]string(nil), report.Paths...)
	clone.ExposedPrivateKeys = append([]string(nil), report.ExposedPrivateKeys...)
	clone.PrivateKeyPaths = append([]string(nil), report.PrivateKeyPaths...)
	clone.Chains = make(map[string][]string, len(report.Chains))
	for path, chain := range report.Chains {
		clone.Chains[path] = append([]string(nil), chain...)
	}
	clone.ByPath = make(map[string]Exposure, len(report.ByPath))
	for path, exposure := range report.ByPath {
		exposure.PathSegments = clonePath(exposure.PathSegments)
		exposure.Chain = append([]string(nil), exposure.Chain...)
		exposure.DerivationChain = append([]string(nil), exposure.DerivationChain...)
		exposure.Steps = cloneSteps(exposure.Steps)
		clone.ByPath[path] = exposure
	}
	clone.Materials = cloneMaterials(report.Materials)
	if report.Tree != nil {
		clone.Tree = treeSnapshotFromPublic(report.Tree)
	}
	return clone
}

func treeSnapshotFromPublic(tree *WalletTree) *WalletTree {
	if tree == nil {
		return tree
	}
	index, err := buildTreeIndex(AuditRequest{Tree: tree})
	if err != nil {
		return tree
	}
	return treeSnapshot(index.root)
}

func sameTree(a, b *WalletTree) bool {
	if a == nil || b == nil {
		return a == b
	}
	left := treeSnapshotFromPublic(a)
	right := treeSnapshotFromPublic(b)
	return sameWalletTree(left, right)
}

func treeIsSubset(small, large *WalletTree) bool {
	smallIndex, err := buildTreeIndex(AuditRequest{Tree: small})
	if err != nil {
		return false
	}
	largeIndex, err := buildTreeIndex(AuditRequest{Tree: large})
	if err != nil {
		return false
	}
	for path := range smallIndex.nodes {
		if _, ok := largeIndex.nodes[path]; !ok {
			return false
		}
	}
	return true
}

func sameWalletTree(a, b *WalletTree) bool {
	if a == nil || b == nil || a.Root == nil || b.Root == nil {
		return a == b
	}
	return sameNode(a.Root, b.Root)
}

func sameNode(a, b *WalletNode) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Path != b.Path || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !sameNode(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

func (request AuditRequest) baselineReport() *AuditReport {
	if request.Baseline != nil {
		return request.Baseline
	}
	if request.Previous != nil {
		return request.Previous
	}
	if request.PreviousReport != nil {
		return request.PreviousReport
	}
	if request.Prior != nil {
		return request.Prior
	}
	return request.LastReport
}

// Baseline returns the optional previous report carried by a request.
func (request AuditRequest) BaselineReport() *AuditReport {
	return request.baselineReport()
}

func (request AuditRequest) BaselineExposed() []string {
	if len(request.PreviouslyExposed) > 0 {
		return append([]string(nil), request.PreviouslyExposed...)
	}
	if len(request.PreviousExposed) > 0 {
		return append([]string(nil), request.PreviousExposed...)
	}
	return append([]string(nil), request.PreviousExposedPaths...)
}
