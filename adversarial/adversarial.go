// Package adversarial contains a bounded adversarial searcher that looks for
// the shortest sequence of container-side operations producing an
// unauthorized host file access.
//
// The searcher exhaustively enumerates every operation sequence up to six
// steps (symlink creation, rename, directory exchange, unlink and open
// attempts), records all interleavings of attacker mutations with a victim's
// component-by-component open, and replays each scenario against both the
// vulnerable prefix resolver and the openat2-semantics secure resolver. A
// finding counts only when the prefix resolver reaches a host node outside
// the container while the secure resolver refuses or stays contained, which
// is exactly the regression contract of the package.
package adversarial

import (
	"fmt"
	"sort"
	"strings"

	"formal.local/w27-6467acc7b4/mount"
	"formal.local/w27-6467acc7b4/resolve"
	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

// MaxSteps is the hard bound on attacker sequence length.
const MaxSteps = 6

// ActionKind enumerates attacker primitives.
type ActionKind uint8

const (
	ActSymlink ActionKind = iota
	ActRename
	ActExchange
	ActUnlink
	ActOpen
)

// String renders the action kind.
func (k ActionKind) String() string {
	switch k {
	case ActSymlink:
		return "symlink"
	case ActRename:
		return "rename"
	case ActExchange:
		return "exchange"
	case ActUnlink:
		return "unlink"
	default:
		return "open"
	}
}

// Action is one attacker step. Semantics depend on Kind:
//
//   - ActSymlink: create link Name in Dir pointing at Target.
//   - ActRename: rename SrcName in SrcDir to DstName in DstDir.
//   - ActExchange: atomically exchange SrcName and DstName.
//   - ActUnlink: remove Name from Dir.
//   - ActOpen: ask the victim resolver to open Path.
type Action struct {
	Kind    ActionKind
	Dir     *vfs.Node
	SrcDir  *vfs.Node
	DstDir  *vfs.Node
	Name    string
	SrcName string
	DstName string
	Target  string
	Path    string
}

func (a Action) String() string {
	switch a.Kind {
	case ActSymlink:
		return fmt.Sprintf("symlink(%s, %q)", vfs.PathOf(a.Dir)+"/"+a.Name, a.Target)
	case ActRename:
		return fmt.Sprintf("rename(%s/%s -> %s/%s)", vfs.PathOf(a.SrcDir), a.SrcName, vfs.PathOf(a.DstDir), a.DstName)
	case ActExchange:
		return fmt.Sprintf("exchange(%s/%s <-> %s/%s)", vfs.PathOf(a.SrcDir), a.SrcName, vfs.PathOf(a.DstDir), a.DstName)
	case ActUnlink:
		return fmt.Sprintf("unlink(%s/%s)", vfs.PathOf(a.Dir), a.Name)
	default:
		return fmt.Sprintf("open(%q)", a.Path)
	}
}

// Finding records one escape demonstrated against the vulnerable resolver.
type Finding struct {
	Sequence     []Action
	Interleaving []int
	OpenPath     string
	HostNode     *vfs.Node
	HostPath     string
	SecureError  error
	SecurePath   string
}

// Report summarizes a search run.
type Report struct {
	StatesVisited int
	Sequences     int
	Interleavings int
	Findings      []Finding
	SecureEscapes int
}

// Scenario is a fully prepared search environment.
type Scenario struct {
	FS            *vfs.VFS
	NS            *mount.Namespace
	ContainerRoot *vfs.Node
	HostSecret    *vfs.Node
	Secure        *resolve.SecureResolver
	Vulnerable    *resolve.PrefixResolver
	Cred          *userns.Cred
	Candidates    []Action
	OpenPaths     []string
	Flags         resolve.Flags
}

// Searcher runs bounded exhaustive searches over a scenario.
type Searcher struct {
	scenario *Scenario
	visited  map[string]bool
	report   Report
}

// NewSearcher binds a scenario.
func NewSearcher(scenario *Scenario) *Searcher {
	return &Searcher{
		scenario: scenario,
		visited:  map[string]bool{},
	}
}

// Run enumerates every attacker sequence up to MaxSteps and every victim
// interleaving. Mutation sequences are explored by DFS with structural state
// deduplication; interleaving evaluations always replay from a pristine
// baseline so each test sees a deterministic filesystem.
func (s *Searcher) Run() Report {
	mutators := make([]Action, 0, len(s.scenario.Candidates))
	for _, action := range s.scenario.Candidates {
		if action.Kind != ActOpen {
			mutators = append(mutators, action)
		}
	}
	// The pristine baseline is itself an evaluation target (zero mutations).
	s.evaluateFromBaseline(nil)
	s.dfs(nil, mutators, MaxSteps)
	return s.report
}

func (s *Searcher) dfs(prefix []Action, mutators []Action, budget int) {
	if budget == 0 {
		return
	}
	for _, action := range mutators {
		if !s.apply(action) {
			continue
		}
		fingerprint := s.fingerprint()
		next := append(append([]Action(nil), prefix...), action)
		alreadyVisited := s.visited[fingerprint]
		if !alreadyVisited {
			s.visited[fingerprint] = true
			s.report.StatesVisited++
			s.evaluateFromBaseline(next)
			s.dfs(next, mutators, budget-1)
		}
		s.rollback(action)
	}
}

// evaluateFromBaseline replays mutations from the pristine baseline and tests
// every interleaving point against each victim open path.
func (s *Searcher) evaluateFromBaseline(mutations []Action) {
	for _, openPath := range s.scenario.OpenPaths {
		for split := 0; split <= len(mutations); split++ {
			s.report.Sequences++
			s.report.Interleavings++
			finding, escaped := s.runOnce(mutations, split, openPath)
			if escaped {
				s.report.Findings = append(s.report.Findings, finding)
			}
		}
	}
}

func (s *Searcher) runOnce(mutations []Action, mutationSplit int, openPath string) (Finding, bool) {
	sc := s.scenario
	// Replay the prefix onto the currently pristine tree.
	for i := 0; i < mutationSplit && i < len(mutations); i++ {
		if !s.apply(mutations[i]) {
			return Finding{}, false
		}
	}

	vulnWalk := sc.Vulnerable.OpenSlow(openPath, sc.Flags, sc.Cred)
	victimSteps := 0
	for {
		if victimSteps == mutationSplit {
			for i := mutationSplit; i < len(mutations); i++ {
				if !s.apply(mutations[i]) {
					return Finding{}, false
				}
			}
		}
		_, done, err := vulnWalk.Step()
		if err != nil {
			return Finding{}, false
		}
		victimSteps++
		if done {
			break
		}
		if victimSteps > 64 {
			return Finding{}, false
		}
	}
	vulnResult, err := vulnWalk.Finish()
	if err != nil || vulnResult == nil {
		return Finding{}, false
	}
	// Undo every mutation so the next evaluation starts from the baseline.
	for i := len(mutations) - 1; i >= 0; i-- {
		s.rollback(mutations[i])
	}

	escaped := !vulnResult.InRoot
	if !escaped {
		// Also flag direct exposure of the designated host secret.
		escaped = vulnResult.Node == sc.HostSecret
	}
	if !escaped {
		return Finding{}, false
	}

	secureResult, secureErr := sc.Secure.Open(openPath, sc.Flags, sc.Cred)
	securePath := ""
	if secureResult != nil {
		securePath = vfs.PathOf(secureResult.Node)
		if !secureResult.InRoot && secureResult.Node != sc.ContainerRoot &&
			!vfs.IsAncestor(sc.ContainerRoot, secureResult.Node) {
			s.report.SecureEscapes++
		}
	}
	return Finding{
		Sequence:     append([]Action(nil), mutations...),
		Interleaving: []int{mutationSplit},
		OpenPath:     openPath,
		HostNode:     vulnResult.Node,
		HostPath:     vfs.PathOf(vulnResult.Node),
		SecureError:  secureErr,
		SecurePath:   securePath,
	}, true
}

func (s *Searcher) apply(action Action) bool {
	sc := s.scenario
	switch action.Kind {
	case ActSymlink:
		_, err := sc.FS.Symlink(action.Dir, action.Name, action.Target, 0, 0)
		return err == nil
	case ActRename:
		return sc.FS.Rename(action.SrcDir, action.SrcName, action.DstDir, action.DstName) == nil
	case ActExchange:
		return sc.FS.Exchange(action.SrcDir, action.SrcName, action.DstDir, action.DstName) == nil
	case ActUnlink:
		// Retained in the action vocabulary for scenario builders; searches
		// restrict candidates to reversible actions so DFS rollback is exact.
		return false
	default:
		return false
	}
}

func (s *Searcher) rollback(action Action) {
	// Rollback uses symmetric reverse operations; repeated failed applies are
	// filtered by apply's boolean result so the search stays consistent.
	sc := s.scenario
	switch action.Kind {
	case ActSymlink:
		_ = sc.FS.Remove(action.Dir, action.Name)
	case ActRename:
		_ = sc.FS.Rename(action.DstDir, action.DstName, action.SrcDir, action.SrcName)
	case ActExchange:
		_ = sc.FS.Exchange(action.SrcDir, action.SrcName, action.DstDir, action.DstName)
	case ActUnlink:
		// Never reached by bounded searches (apply rejects unlink actions).
	}
}

// fingerprint renders the reachable tree as a deterministic string so that
// different sequences reaching the same state are enumerated once.
func (s *Searcher) fingerprint() string {
	var b strings.Builder
	s.fingerprintNode(&b, s.scenario.FS.HostRoot, 0)
	return b.String()
}

func (s *Searcher) fingerprintNode(b *strings.Builder, node *vfs.Node, depth int) {
	for i := 0; i < depth; i++ {
		b.WriteByte(' ')
	}
	fmt.Fprintf(b, "%s[%d]:%s", node.Name(), node.ID(), kindName(node.Kind()))
	if node.Kind() == vfs.KindSymlink {
		fmt.Fprintf(b, "->%s", node.Target())
	}
	if node.Kind() == vfs.KindMagicLink {
		b.WriteString("->magic:")
		b.WriteString(node.Magic().Resolve())
	}
	b.WriteByte('\n')
	children := node.SnapshotChildren()
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s.fingerprintNode(b, children[name], depth+1)
	}
}

func kindName(k vfs.Kind) string {
	switch k {
	case vfs.KindDir:
		return "d"
	case vfs.KindFile:
		return "f"
	case vfs.KindSymlink:
		return "l"
	default:
		return "m"
	}
}
