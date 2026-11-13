package netsec

import (
	"fmt"
	"strings"
)

// IssueCode identifies a rejection reason.
type IssueCode string

const (
	CodeBadNameTable   IssueCode = "bad-name-table"
	CodeDuplicateName  IssueCode = "duplicate-name"
	CodeMalformedTable IssueCode = "malformed-table"
	CodeBadIndex       IssueCode = "bad-index"
	CodeTruncatedChain IssueCode = "truncated-chain"
	CodeChainCycle     IssueCode = "chain-cycle"
	CodeChainOverlap   IssueCode = "chain-overlap"
	CodeChainMismatch  IssueCode = "chain-mismatch"
	CodeWorkBudget     IssueCode = "work-budget"
	CodeMissingExport  IssueCode = "missing-export"
	CodeMisboundExport IssueCode = "misbound-export"
	CodeIndexDisagrees IssueCode = "index-disagreement"
	CodeLookupError    IssueCode = "lookup-error"
)

// Issue describes one reason a table pair was rejected. Name and Index point at
// the affected export when one is known; GNU and SysV contain the lookup paths
// that led to the conclusion when a lookup was performed.
type Issue struct {
	Code   IssueCode
	Name   string
	Index  int
	Detail string
	GNU    *GNUPath
	SysV   *SysVPath
}

func (i Issue) Error() string {
	var b strings.Builder
	b.WriteString(string(i.Code))
	if i.Index >= 0 {
		fmt.Fprintf(&b, " at #%d", i.Index)
	}
	if i.Name != "" {
		fmt.Fprintf(&b, " %q", i.Name)
	}
	if i.Detail != "" {
		b.WriteString(": ")
		b.WriteString(i.Detail)
	}
	if i.GNU != nil {
		b.WriteString("; ")
		b.WriteString(i.GNU.String())
	}
	if i.SysV != nil {
		b.WriteString("; ")
		b.WriteString(i.SysV.String())
	}
	return b.String()
}

// SymbolCheck records the dual-index lookup performed for one export.
type SymbolCheck struct {
	Index uint32
	Name  string
	GNU   *LookupResult
	SysV  *LookupResult
}

func (c SymbolCheck) String() string {
	gnu := "gnu=<not-covered>"
	if c.GNU != nil {
		gnu = c.GNU.String()
	}
	sysv := "sysv=<not-run>"
	if c.SysV != nil {
		sysv = c.SysV.String()
	}
	return fmt.Sprintf("#%d %q: %s; %s", c.Index, c.Name, gnu, sysv)
}

// NameCheck records both lookup paths for an arbitrary name, whether or not it
// is an export. It is useful for reviewing Bloom false positives without
// treating them as attacks.
type NameCheck struct {
	Name        string
	LinearIndex uint32
	LinearFound bool
	GNU         LookupResult
	SysV        LookupResult
}

func (c NameCheck) String() string {
	linear := "not-found"
	if c.LinearFound {
		linear = fmt.Sprintf("#%d", c.LinearIndex)
	}
	return fmt.Sprintf("%q: linear=%s; %s; %s", c.Name, linear, c.GNU.String(), c.SysV.String())
}

// Report is the result of verifying a GNU/System V table pair.
type Report struct {
	OK         bool
	Issues     []Issue
	Checks     []SymbolCheck
	Work       uint64
	WorkBudget uint64
}

// Err returns nil for a clean report and a VerificationError otherwise.
func (r *Report) Err() error {
	if r == nil {
		return fmt.Errorf("nil verification report")
	}
	if r.OK {
		return nil
	}
	return &VerificationError{Report: r}
}

func (r *Report) summary() string {
	if r == nil {
		return "nil verification report"
	}
	if r.OK {
		return fmt.Sprintf("dual hash index verification passed (%d exports, %d work units)",
			len(r.Checks), r.Work)
	}
	return fmt.Sprintf("dual hash index verification failed: %d issue(s) across %d export(s)",
		len(r.Issues), len(r.Checks))
}

// String renders a one-line summary of the report.
func (r *Report) String() string {
	return r.summary()
}

// VerificationError wraps a failed Report so it can be returned as an error.
type VerificationError struct {
	Report *Report
}

func (e *VerificationError) Error() string {
	if e == nil || e.Report == nil {
		return "dual hash index verification failed"
	}
	var b strings.Builder
	b.WriteString(e.Report.summary())
	const maxIssues = 8
	for i, issue := range e.Report.Issues {
		if i == maxIssues {
			fmt.Fprintf(&b, "; ... and %d more", len(e.Report.Issues)-maxIssues)
			break
		}
		b.WriteString("; ")
		b.WriteString(issue.Error())
	}
	return b.String()
}

// ReportForError exposes the underlying report without implementing the
// conventional errors.Unwrap contract (a Report is not itself an error).
func (e *VerificationError) ReportForError() *Report {
	if e == nil {
		return nil
	}
	return e.Report
}
