package rebuild

import (
	"fmt"
	"sort"
	"strings"
)

// ValidationReport 以源库全扫描为参考，对比索引全扫描的结果。
type ValidationReport struct {
	Checked   int
	Missing   []string // 源库存活而索引缺失
	Extra     []string // 源库已删而索引仍存活
	Different []string // 值或版本不一致
}

func (r ValidationReport) OK() bool {
	return len(r.Missing) == 0 && len(r.Extra) == 0 && len(r.Different) == 0
}

func (r ValidationReport) String() string {
	if r.OK() {
		return fmt.Sprintf("ok (%d keys checked)", r.Checked)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "MISMATCH (%d keys checked)", r.Checked)
	for _, k := range r.Missing {
		fmt.Fprintf(&b, "\n    missing in index: %s", k)
	}
	for _, k := range r.Extra {
		fmt.Fprintf(&b, "\n    stale in index (deleted in source): %s", k)
	}
	for _, d := range r.Different {
		fmt.Fprintf(&b, "\n    diverged: %s", d)
	}
	return b.String()
}

// Validate 全量扫描源库与索引并报告所有分歧。
func Validate(src *Source, idx *Index) ValidationReport {
	ref := src.Scan()
	got := idx.Scan()
	rep := ValidationReport{Checked: len(ref)}
	for k, want := range ref {
		have, ok := got[k]
		switch {
		case !ok:
			rep.Missing = append(rep.Missing, k)
		case have.Value != want.Value || have.Version != want.Version:
			rep.Different = append(rep.Different, fmt.Sprintf(
				"%s (source v%d=%q, index v%d=%q)", k, want.Version, want.Value, have.Version, have.Value))
		}
	}
	for k := range got {
		if _, ok := ref[k]; !ok {
			rep.Extra = append(rep.Extra, k)
		}
	}
	sort.Strings(rep.Missing)
	sort.Strings(rep.Extra)
	sort.Strings(rep.Different)
	return rep
}

// Evidence 记录一次切换尝试的水位与门控结果，作为新旧索引水位证据。
type Evidence struct {
	FreezeVersion uint64
	SourceVersion uint64
	OldWatermark  uint64
	NewWatermark  uint64
	Unacked       map[string]uint64
	Validated     bool
	Validation    ValidationReport
	Switched      bool
	Reason        string
}

func (e Evidence) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "freeze version : %d\n", e.FreezeVersion)
	fmt.Fprintf(&b, "source version : %d\n", e.SourceVersion)
	fmt.Fprintf(&b, "old index mark : %d\n", e.OldWatermark)
	fmt.Fprintf(&b, "new index mark : %d\n", e.NewWatermark)
	if len(e.Unacked) > 0 {
		keys := make([]string, 0, len(e.Unacked))
		for k, v := range e.Unacked {
			keys = append(keys, fmt.Sprintf("%s@v%d", k, v))
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "unacked        : %s\n", strings.Join(keys, ", "))
	} else {
		b.WriteString("unacked        : none\n")
	}
	if e.Validated {
		fmt.Fprintf(&b, "validation     : %s\n", e.Validation.String())
	} else {
		b.WriteString("validation     : not run\n")
	}
	if e.Switched {
		fmt.Fprintf(&b, "decision       : SWITCHED (%s)", e.Reason)
	} else {
		fmt.Fprintf(&b, "decision       : REFUSED (%s)", e.Reason)
	}
	return b.String()
}
