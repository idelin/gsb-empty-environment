package displaycommit

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type Verdict string

const (
	VerdictUnknown Verdict = "unknown"
	VerdictSafe    Verdict = "safe"
	VerdictUnsafe  Verdict = "unsafe"

	StatusUnknown = VerdictUnknown
	StatusSafe    = VerdictSafe
	StatusUnsafe  = VerdictUnsafe

	ResultUnknown = VerdictUnknown
	ResultSafe    = VerdictSafe
	ResultUnsafe  = VerdictUnsafe

	StatusProven      = VerdictSafe
	VerdictProven     = VerdictSafe
	StatusViolation   = VerdictUnsafe
	VerdictViolation  = VerdictUnsafe
	StatusCannotProve = VerdictUnknown
	VerdictUnprovable = VerdictUnknown
)

type Difference struct {
	Path        string `json:"path"`
	Left        string `json:"left,omitempty"`
	Right       string `json:"right,omitempty"`
	Authorized  bool   `json:"authorized"`
	Role        Role   `json:"role,omitempty"`
	Description string `json:"description,omitempty"`
}

type Counterexample struct {
	LeftIndex   int          `json:"left_index"`
	RightIndex  int          `json:"right_index"`
	ScreenKey   string       `json:"screen_key"`
	Differences []Difference `json:"differences"`
	Reason      string       `json:"reason,omitempty"`
}

type BindingInterval struct {
	Role     Role     `json:"role,omitempty"`
	Path     string   `json:"path,omitempty"`
	Asset    string   `json:"asset,omitempty"`
	Interval Interval `json:"interval"`
}

type MergeReport struct {
	Label           string   `json:"label"`
	Asset           string   `json:"asset,omitempty"`
	SourcePaths     []string `json:"source_paths"`
	IdentityVisible bool     `json:"identity_visible"`
}

type Report struct {
	Verdict Verdict `json:"verdict"`
	Status  Verdict `json:"status"`
	Safe    bool    `json:"safe"`
	Unknown bool    `json:"unknown"`
	Unsafe  bool    `json:"unsafe"`

	Exhaustive bool     `json:"exhaustive,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`

	Displays        []*Display        `json:"-"`
	Counterexamples []Counterexample  `json:"counterexamples,omitempty"`
	Intervals       []BindingInterval `json:"intervals,omitempty"`
	Merges          []MergeReport     `json:"merges,omitempty"`
	ScreenCount     int               `json:"screen_count"`
	MessageCount    int               `json:"message_count"`

	Err error `json:"-"`
}

type VerifyOptions struct {
	// Exhaustive asserts that Messages form the complete legal small domain.
	Exhaustive bool
	// RequireProofs makes summarized/rounded screens Unknown unless exact
	// commitments and page proofs are supplied and verified separately.
	RequireProofs bool
}

// VerifyRule renders all messages and compares equal screens pairwise.
func VerifyRule(table *TypeTable, rule *DisplayRule, messages []*Message, opts ...VerifyOptions) *Report {
	opt := VerifyOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}
	rep := &Report{Verdict: VerdictUnknown, Status: VerdictUnknown, Unknown: true, Exhaustive: opt.Exhaustive, MessageCount: len(messages)}
	if table == nil {
		rep.addUnknown("missing type table")
		return rep.finalize()
	}
	if rule == nil {
		rep.addUnknown("missing display rule")
		return rep.finalize()
	}
	if len(messages) == 0 {
		rep.addUnknown("empty message domain")
		return rep.finalize()
	}

	allow, err := rule.nonAuthorizedNormalized()
	if err != nil {
		rep.Err = err
		rep.addUnknown("invalid non-authorized path: " + err.Error())
		return rep.finalize()
	}

	if ok, bad := table.Bounded(); !ok {
		rep.Reasons = append(rep.Reasons, "unbounded repeated field "+bad.Key())
		if !opt.Exhaustive {
			return rep.finalize()
		}
	}

	displays := make([]*Display, len(messages))
	keys := make([]string, len(messages))
	for i, msg := range messages {
		if msg == nil {
			rep.Err = fmt.Errorf("displaycommit: message %d is nil", i)
			rep.addUnknown(fmt.Sprintf("message %d is nil", i))
			return rep.finalize()
		}
		d, err := Render(table, msg, rule)
		if err != nil {
			rep.Err = err
			rep.addUnknown("render failed: " + err.Error())
			return rep.finalize()
		}
		displays[i] = d
		keys[i] = d.VisualKey()
	}
	rep.Displays = displays
	rep.ScreenCount = len(uniqueStrings(keys))
	rep.collectIntervals(table, messages)
	rep.collectMerges(displays)

	leafMaps := make([]map[string]Leaf, len(messages))
	for i, m := range messages {
		leafMaps[i] = m.LeafMap()
	}

	for i := 0; i < len(messages); i++ {
		for j := i + 1; j < len(messages); j++ {
			if keys[i] != keys[j] {
				continue
			}
			allDiffs := compareLeaves(leafMaps[i], leafMaps[j], allow, table, messages[i], messages[j])
			var unauthorized []Difference
			for _, d := range allDiffs {
				if d.Authorized {
					unauthorized = append(unauthorized, d)
				}
			}
			if len(unauthorized) > 0 {
				ce := Counterexample{LeftIndex: i, RightIndex: j, ScreenKey: keys[i], Differences: unauthorized}
				if d := coreRoleDifference(unauthorized); d != nil {
					ce.Reason = "authorized binding differs: " + string(d.Role) + " at " + d.Path
				} else {
					ce.Reason = "screen-equal messages differ on a field not declared non-authorized"
				}
				rep.Counterexamples = append(rep.Counterexamples, ce)
			}
		}
	}

	if len(rep.Counterexamples) > 0 {
		rep.Verdict = VerdictUnsafe
		rep.Status = VerdictUnsafe
		rep.Unsafe = true
		rep.Unknown = false
		return rep.finalize()
	}

	// Even without an observed collision, non-injective rounding is not a
	// general proof unless the caller asserts an exhaustive finite domain or
	// exact inclusion proofs are required/bound.
	if !opt.Exhaustive && ruleHasUnprovenRounding(displays) && !opt.RequireProofs {
		rep.addUnknown("rounded amount screen has a non-singleton exact-value interval")
		return rep.finalize()
	}
	if !opt.Exhaustive {
		rep.addUnknown("message set is a sample, not an exhaustive small domain")
		return rep.finalize()
	}

	rep.Verdict = VerdictSafe
	rep.Status = VerdictSafe
	rep.Safe = true
	rep.Unknown = false
	return rep.finalize()
}

func (r *Report) finalize() *Report {
	r.Safe = r.Verdict == VerdictSafe
	r.Unsafe = r.Verdict == VerdictUnsafe
	r.Unknown = r.Verdict == VerdictUnknown || r.Verdict == ""
	if r.Verdict == "" {
		r.Verdict = VerdictUnknown
		r.Status = r.Verdict
	}
	return r
}

func (r *Report) addUnknown(reason string) {
	r.Verdict = VerdictUnknown
	r.Status = VerdictUnknown
	r.Reasons = append(r.Reasons, reason)
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func pathAllowed(pathKey string, allow []Path) bool {
	p, err := NormalizePath(pathKey)
	if err != nil {
		return false
	}
	for _, a := range allow {
		if len(a) == 0 || len(a) > len(p) {
			continue
		}
		ok := true
		for i := range a {
			if a[i].Field != p[i].Field {
				ok = false
				break
			}
			if a[i].Index >= 0 && a[i].Index != p[i].Index {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func compareLeaves(a, b map[string]Leaf, allow []Path, table *TypeTable, ma, mb *Message) []Difference {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var diffs []Difference
	for _, k := range sorted {
		la, oka := a[k]
		lb, okb := b[k]
		if oka && okb && la.CommitmentString() == lb.CommitmentString() {
			continue
		}
		authorized := !pathAllowed(k, allow)
		role := roleForPath(table, ma, k)
		d := Difference{Path: k, Authorized: authorized, Role: role}
		if oka {
			d.Left = la.CommitmentString()
		}
		if okb {
			d.Right = lb.CommitmentString()
		}
		if !oka || !okb {
			d.Description = "presence differs (missing versus explicit value)"
		} else {
			d.Description = "value commitment differs"
		}
		diffs = append(diffs, d)
	}
	return diffs
}

func roleForPath(table *TypeTable, msg *Message, pathKey string) Role {
	if table == nil {
		return ""
	}
	p, err := NormalizePath(pathKey)
	if err != nil || len(p) == 0 {
		return ""
	}
	spec, ok := table.SpecAtPath(msg.typeName(), p)
	if !ok {
		return ""
	}
	for _, r := range spec.allRoles() {
		if r != RoleNonAuth {
			return r
		}
	}
	return ""
}

func coreRoleDifference(diffs []Difference) *Difference {
	core := map[Role]bool{RolePayee: true, RoleAsset: true, RoleAmount: true, RoleDomain: true, RoleDeadline: true}
	for i := range diffs {
		if core[canonicalRole(diffs[i].Role)] && diffs[i].Authorized {
			d := diffs[i]
			d.Role = canonicalRole(d.Role)
			return &d
		}
	}
	// If table role metadata was unavailable, any unauthorized difference is
	// still reported; callers should provide the normalized table for precise
	// role labels.
	for i := range diffs {
		if diffs[i].Authorized {
			return &diffs[i]
		}
	}
	return nil
}

func (r *Report) collectIntervals(table *TypeTable, msgs []*Message) {
	if table == nil {
		return
	}
	for _, role := range []Role{RoleAmount} {
		paths := table.RolePaths(role)
		for _, p := range paths {
			var bigs []*big.Int
			asset := ""
			for _, m := range msgs {
				rs, err := m.Resolve(p)
				if err != nil {
					continue
				}
				for _, x := range rs {
					bigs = append(bigs, valueInt(x.Value))
				}
			}
			if len(bigs) > 0 {
				iv := IntervalOfValues(bigs...)
				r.Intervals = append(r.Intervals, BindingInterval{Role: role, Path: p.Key(), Asset: asset, Interval: iv})
			}
		}
	}
}

func (r *Report) collectMerges(displays []*Display) {
	seen := map[string]bool{}
	for _, d := range displays {
		for _, p := range d.Pages {
			for _, l := range p.Lines {
				if !l.Merged {
					continue
				}
				key := l.Label + "|" + strings.Join(l.SourcePaths, ",")
				if seen[key] {
					continue
				}
				seen[key] = true
				r.Merges = append(r.Merges, MergeReport{Label: l.Label, Asset: l.Asset, SourcePaths: l.SourcePaths, IdentityVisible: false})
			}
		}
	}
}

func ruleHasUnprovenRounding(displays []*Display) bool {
	for _, d := range displays {
		for _, p := range d.Pages {
			for _, l := range p.Lines {
				if l.Interval.Lo != nil && !l.Interval.IsPoint() {
					return true
				}
			}
		}
	}
	return false
}

// Verify is the primary concise entry point. Explicit messages are treated as
// the complete small domain unless VerifyOptions{Exhaustive:false} is
// supplied. Use VerifySample for non-exhaustive sets.
func Verify(table *TypeTable, rule *DisplayRule, messages []*Message, options ...VerifyOptions) *Report {
	opt := VerifyOptions{Exhaustive: true}
	if len(options) > 0 {
		opt = options[0]
	}
	return VerifyRule(table, rule, messages, opt)
}

// VerifySample never generalizes a clean pairwise result to all legal
// messages. Rounding or other lossy rules therefore remain Unknown unless an
// explicit proof path establishes exact-value binding.
func VerifySample(table *TypeTable, rule *DisplayRule, messages []*Message) *Report {
	return VerifyRule(table, rule, messages, VerifyOptions{Exhaustive: false})
}

// VerifySmallDomain treats messages as the complete finite legal domain and
// performs exhaustive pairwise screen comparison.
func VerifySmallDomain(table *TypeTable, rule *DisplayRule, messages []*Message) *Report {
	return VerifyRule(table, rule, messages, VerifyOptions{Exhaustive: true})
}

func CheckSmallDomain(table *TypeTable, rule *DisplayRule, messages ...*Message) *Report {
	return VerifySmallDomain(table, rule, messages)
}

// ValidateRule returns nil only for a proven exhaustive small domain.
func ValidateRule(table *TypeTable, rule *DisplayRule, messages []*Message) error {
	rep := VerifySmallDomain(table, rule, messages)
	if rep.Err != nil {
		return rep.Err
	}
	if !rep.Safe {
		if len(rep.Counterexamples) > 0 {
			return fmt.Errorf("displaycommit: rule violates binding: %s", rep.Counterexamples[0].Reason)
		}
		return fmt.Errorf("displaycommit: rule not provable: %s", strings.Join(rep.Reasons, "; "))
	}
	return nil
}

// IsUnknown is a convenience for callers using a boolean error-style API.
func IsUnknown(rep *Report) bool { return rep == nil || rep.Unknown }
