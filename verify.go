package netsec

import (
	"errors"
	"fmt"
)

// VerifyOptions controls resource limits for verification. The zero value is
// replaced by a size-dependent default when Verify is called.
type VerifyOptions struct {
	MaxWork uint64
}

// Option customizes verification.
type Option func(*VerifyOptions)

// WithWorkBudget sets an explicit upper bound on chain-walk work.
func WithWorkBudget(max uint64) Option {
	return func(o *VerifyOptions) {
		if max != 0 {
			o.MaxWork = max
		}
	}
}

// Verifier groups the untrusted inputs.
type Verifier struct {
	Names []string
	GNU   *GNUTable
	SysV  *SysVTable
}

// Verify checks names against table.
func (v Verifier) Verify(opts ...Option) *Report {
	return Verify(v.Names, v.GNU, v.SysV, opts...)
}

// Check verifies names against table and returns an error on rejection.
func (v Verifier) Check(opts ...Option) error {
	return v.Verify(opts...).Err()
}

// Verify checks this GNU table against a System V table and the symbol names.
func (t *GNUTable) Verify(names []string, sysv *SysVTable, opts ...Option) *Report {
	return Verify(names, t, sysv, opts...)
}

// Verify checks this System V table against a GNU table and the symbol names.
func (t *SysVTable) Verify(names []string, gnu *GNUTable, opts ...Option) *Report {
	return Verify(names, gnu, t, opts...)
}

// Verify checks that the GNU and System V indexes agree with each other and
// with a linear name lookup for every export they are required to cover.
//
// GNU must cover SymOffset through the end of the symbol table. System V must
// cover every nonempty symbol. The verifier rejects malformed array lengths,
// out-of-range indexes, cycles, overlapping or truncated chains, hash/bucket
// mismatches, hidden exports, and exports rebound to a different symbol.
//
// A Bloom false positive is not an attack: Verify does not require Bloom bits
// to be clear for names that are not exports. A missing Bloom bit for an
// export is caught because the GNU lookup cannot find that export.
func Verify(names []string, gnu *GNUTable, sysv *SysVTable, opts ...Option) *Report {
	config := VerifyOptions{MaxWork: defaultWorkBudget(len(names))}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}
	if config.MaxWork == 0 {
		config.MaxWork = defaultWorkBudget(len(names))
	}

	report := &Report{WorkBudget: config.MaxWork}
	checker := verifier{
		names:  names,
		gnu:    gnu,
		sysv:   sysv,
		report: report,
		budget: config.MaxWork,
		gnuOK:  true,
		sysvOK: true,
	}

	checker.checkNames()
	if err := gnu.validate(len(names)); err != nil {
		checker.gnuOK = false
		checker.addIssue(Issue{Code: CodeMalformedTable, Index: -1, Detail: "GNU: " + err.Error()})
	}
	if err := sysv.validate(len(names)); err != nil {
		checker.sysvOK = false
		checker.addIssue(Issue{Code: CodeMalformedTable, Index: -1, Detail: "System V: " + err.Error()})
	}

	if checker.gnuOK {
		checker.walkGNU()
	}
	if checker.sysvOK {
		checker.walkSysV()
	}
	if !checker.stopped {
		checker.checkExports()
	}
	checker.fillIssuePaths()

	report.OK = len(report.Issues) == 0 && !checker.stopped
	return report
}

// VerifyDual is a convenience wrapper returning both the detailed report and a
// rejection error.
func VerifyDual(names []string, gnu *GNUTable, sysv *SysVTable, opts ...Option) (*Report, error) {
	report := Verify(names, gnu, sysv, opts...)
	return report, report.Err()
}

// Check verifies the pair and returns only an error.
func Check(names []string, gnu *GNUTable, sysv *SysVTable, opts ...Option) error {
	return Verify(names, gnu, sysv, opts...).Err()
}

// CheckName reports both lookup paths for an arbitrary name. It does not claim
// that a name is an export; use LinearFound for that.
func CheckName(names []string, name string, gnu *GNUTable, sysv *SysVTable) NameCheck {
	index, found := LinearLookup(names, name)
	return NameCheck{
		Name:        name,
		LinearIndex: index,
		LinearFound: found,
		GNU:         gnu.Lookup(names, name),
		SysV:        sysv.Lookup(names, name),
	}
}

type verifier struct {
	names     []string
	gnu       *GNUTable
	sysv      *SysVTable
	report    *Report
	budget    uint64
	gnuOK     bool
	sysvOK    bool
	stopped   bool
	issueKeys map[string]struct{}
}

func defaultWorkBudget(symbolCount int) uint64 {
	const minimum = uint64(1) << 20
	n := uint64(symbolCount) + 1
	if n > 1<<30 {
		return ^uint64(0)
	}
	budget := 4*n*n + 65536
	if budget < minimum {
		return minimum
	}
	return budget
}

func (v *verifier) charge(work uint64) bool {
	if work == 0 {
		return true
	}
	if v.report.Work > ^uint64(0)-work {
		v.budgetExceeded()
		return false
	}
	v.report.Work += work
	if v.report.Work > v.budget {
		v.budgetExceeded()
		return false
	}
	return true
}

func (v *verifier) budgetExceeded() {
	v.addIssue(Issue{
		Code:   CodeWorkBudget,
		Index:  -1,
		Detail: fmt.Sprintf("verification exceeded work budget %d", v.budget),
	})
	v.stopped = true
}

func (v *verifier) addIssue(issue Issue) {
	if v.issueKeys == nil {
		v.issueKeys = make(map[string]struct{})
	}
	key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", issue.Code, issue.Index, issue.Name, issue.Detail)
	if _, ok := v.issueKeys[key]; ok {
		return
	}
	v.issueKeys[key] = struct{}{}
	v.report.Issues = append(v.report.Issues, issue)
}

func (v *verifier) checkNames() {
	if len(v.names) == 0 {
		v.addIssue(Issue{
			Code:   CodeBadNameTable,
			Index:  -1,
			Detail: "symbol name table is empty; index 0 must be the empty sentinel",
		})
		return
	}
	if v.names[0] != "" {
		v.addIssue(Issue{
			Code:   CodeBadNameTable,
			Index:  0,
			Name:   v.names[0],
			Detail: "symbol 0 must have an empty name",
		})
	}

	seen := make(map[string]uint32, len(v.names))
	for i := 1; i < len(v.names); i++ {
		name := v.names[i]
		if name == "" {
			continue
		}
		if previous, ok := seen[name]; ok {
			v.addIssue(Issue{
				Code:   CodeDuplicateName,
				Index:  i,
				Name:   name,
				Detail: fmt.Sprintf("nonempty name also appears at symbol #%d", previous),
			})
			continue
		}
		seen[name] = uint32(i)
	}
}

func (v *verifier) walkGNU() {
	table := v.gnu
	seen := make(map[uint32]uint32)
	maxSteps := len(table.Chains) + 1

	for bucket := uint32(0); bucket < table.NBuckets; bucket++ {
		if !v.charge(1) {
			return
		}
		start := table.Buckets[bucket]
		if start == 0 {
			continue
		}
		if start < table.SymOffset {
			v.addIssue(Issue{
				Code:   CodeBadIndex,
				Index:  int(start),
				Detail: fmt.Sprintf("GNU bucket %d starts below symoffset %d", bucket, table.SymOffset),
			})
			continue
		}
		if uint64(start) >= uint64(len(v.names)) {
			v.addIssue(Issue{
				Code:   CodeBadIndex,
				Index:  int(start),
				Detail: fmt.Sprintf("GNU bucket %d starts outside symbol table", bucket),
			})
			continue
		}

		steps := 0
		for i := start; ; i++ {
			steps++
			if steps > maxSteps {
				v.addIssue(Issue{
					Code:   CodeWorkBudget,
					Index:  int(i),
					Detail: fmt.Sprintf("GNU bucket %d chain exceeded %d steps", bucket, maxSteps),
				})
				return
			}
			if !v.charge(1) {
				return
			}
			if uint64(i) >= uint64(len(v.names)) {
				v.addIssue(Issue{
					Code:   CodeTruncatedChain,
					Index:  int(i),
					Detail: fmt.Sprintf("GNU bucket %d chain runs past symbol table", bucket),
				})
				break
			}
			chainIndex := i - table.SymOffset
			if uint64(chainIndex) >= uint64(len(table.Chains)) {
				v.addIssue(Issue{
					Code:   CodeTruncatedChain,
					Index:  int(i),
					Detail: fmt.Sprintf("GNU bucket %d chain index %d runs past chains", bucket, chainIndex),
				})
				break
			}
			if previousBucket, ok := seen[i]; ok {
				v.addIssue(Issue{
					Code:   CodeChainOverlap,
					Index:  int(i),
					Name:   v.names[i],
					Detail: fmt.Sprintf("GNU symbol is reachable from buckets %d and %d", previousBucket, bucket),
				})
				break
			}
			seen[i] = bucket

			name := v.names[i]
			hash := GNUHash(name)
			expectedBucket := hash % table.NBuckets
			if name != "" && expectedBucket != bucket {
				v.addIssue(Issue{
					Code:   CodeChainMismatch,
					Index:  int(i),
					Name:   name,
					Detail: fmt.Sprintf("GNU hash bucket is %d but symbol is in bucket %d", expectedBucket, bucket),
				})
			}

			chain := table.Chains[chainIndex]
			if chain&^1 != hash&^1 {
				v.addIssue(Issue{
					Code:   CodeChainMismatch,
					Index:  int(i),
					Name:   name,
					Detail: fmt.Sprintf("GNU chain word 0x%08x does not match name hash 0x%08x", chain, hash),
				})
			}
			if chain&1 != 0 {
				break
			}
			if i == ^uint32(0) {
				v.addIssue(Issue{
					Code:   CodeBadIndex,
					Index:  -1,
					Detail: "GNU symbol index overflow",
				})
				return
			}
		}
	}
}

func (v *verifier) walkSysV() {
	table := v.sysv
	bucketCount := uint32(len(table.Buckets))
	seen := make(map[uint32]uint32)
	maxSteps := len(table.Next) + 1

	for bucket := uint32(0); bucket < bucketCount; bucket++ {
		if !v.charge(1) {
			return
		}
		start := table.Buckets[bucket]
		if start == 0 {
			continue
		}
		local := make(map[uint32]struct{})
		steps := 0
		for i := start; i != 0; {
			steps++
			if steps > maxSteps {
				v.addIssue(Issue{
					Code:   CodeWorkBudget,
					Index:  int(i),
					Detail: fmt.Sprintf("System V bucket %d chain exceeded %d steps", bucket, maxSteps),
				})
				return
			}
			if !v.charge(1) {
				return
			}
			if uint64(i) >= uint64(len(v.names)) || uint64(i) >= uint64(len(table.Next)) {
				v.addIssue(Issue{
					Code:   CodeBadIndex,
					Index:  int(i),
					Detail: fmt.Sprintf("System V bucket %d chain contains an out-of-range symbol", bucket),
				})
				break
			}
			if _, ok := local[i]; ok {
				v.addIssue(Issue{
					Code:   CodeChainCycle,
					Index:  int(i),
					Name:   v.names[i],
					Detail: fmt.Sprintf("System V bucket %d chain revisits this symbol", bucket),
				})
				break
			}
			if previousBucket, ok := seen[i]; ok {
				v.addIssue(Issue{
					Code:   CodeChainOverlap,
					Index:  int(i),
					Name:   v.names[i],
					Detail: fmt.Sprintf("System V symbol is reachable from buckets %d and %d", previousBucket, bucket),
				})
				break
			}
			local[i] = struct{}{}
			seen[i] = bucket

			name := v.names[i]
			if name != "" {
				expectedBucket := SysVHash(name) % bucketCount
				if expectedBucket != bucket {
					v.addIssue(Issue{
						Code:   CodeChainMismatch,
						Index:  int(i),
						Name:   name,
						Detail: fmt.Sprintf("System V hash bucket is %d but symbol is in bucket %d", expectedBucket, bucket),
					})
				}
			}
			i = table.Next[i]
		}
	}
}

func (v *verifier) checkExports() {
	for i := 1; i < len(v.names); i++ {
		name := v.names[i]
		if name == "" {
			continue
		}
		expected := uint32(i)
		check := SymbolCheck{Index: expected, Name: name}

		if v.sysvOK {
			result := v.sysv.Lookup(v.names, name)
			check.SysV = &result
			if !v.charge(uint64(len(result.SysV.Steps))) {
				return
			}
		}
		if v.gnuOK && expected >= v.gnu.SymOffset {
			result := v.gnu.Lookup(v.names, name)
			check.GNU = &result
			if !v.charge(uint64(len(result.GNU.Steps))) {
				return
			}
		}

		var gnuPath *GNUPath
		var sysvPath *SysVPath
		if check.GNU != nil {
			gnuPath = check.GNU.GNU
		}
		if check.SysV != nil {
			sysvPath = check.SysV.SysV
		}
		if check.SysV != nil {
			v.checkOneLookup("System V", check.SysV, expected, gnuPath, sysvPath)
		}
		if check.GNU != nil {
			v.checkOneLookup("GNU", check.GNU, expected, gnuPath, sysvPath)
		}
		if check.GNU != nil && check.SysV != nil &&
			check.GNU.Err == nil && check.SysV.Err == nil &&
			check.GNU.Found && check.SysV.Found &&
			check.GNU.Index != check.SysV.Index {
			v.addIssue(Issue{
				Code:  CodeIndexDisagrees,
				Name:  name,
				Index: int(expected),
				Detail: fmt.Sprintf("GNU returned #%d while System V returned #%d",
					check.GNU.Index, check.SysV.Index),
				GNU:  check.GNU.GNU,
				SysV: check.SysV.SysV,
			})
		}
		v.report.Checks = append(v.report.Checks, check)
	}
}

func (v *verifier) checkOneLookup(side string, result *LookupResult, expected uint32, gnuPath *GNUPath, sysvPath *SysVPath) {
	if result.Err != nil {
		code := CodeLookupError
		switch {
		case errors.Is(result.Err, ErrChainCycle):
			code = CodeChainCycle
		case errors.Is(result.Err, ErrWorkBudget):
			code = CodeWorkBudget
		case errors.Is(result.Err, ErrBadIndex):
			code = CodeBadIndex
		case errors.Is(result.Err, ErrMalformedTable):
			code = CodeMalformedTable
		}
		v.addIssue(Issue{
			Code:   code,
			Name:   v.names[expected],
			Index:  int(expected),
			Detail: side + " lookup failed: " + result.Err.Error(),
			GNU:    gnuPath,
			SysV:   sysvPath,
		})
		return
	}
	if !result.Found {
		v.addIssue(Issue{
			Code:   CodeMissingExport,
			Name:   v.names[expected],
			Index:  int(expected),
			Detail: side + " index hid the export",
			GNU:    gnuPath,
			SysV:   sysvPath,
		})
		return
	}
	if result.Index != expected {
		v.addIssue(Issue{
			Code:   CodeMisboundExport,
			Name:   v.names[expected],
			Index:  int(expected),
			Detail: fmt.Sprintf("%s index returned #%d", side, result.Index),
			GNU:    gnuPath,
			SysV:   sysvPath,
		})
	}
}

func (v *verifier) fillIssuePaths() {
	checks := make(map[int]SymbolCheck, len(v.report.Checks))
	for _, check := range v.report.Checks {
		checks[int(check.Index)] = check
	}
	for i := range v.report.Issues {
		issue := &v.report.Issues[i]
		if issue.GNU != nil && issue.SysV != nil {
			continue
		}
		check, ok := checks[issue.Index]
		if !ok {
			continue
		}
		if issue.GNU == nil && check.GNU != nil {
			issue.GNU = check.GNU.GNU
		}
		if issue.SysV == nil && check.SysV != nil {
			issue.SysV = check.SysV.SysV
		}
	}
}
