package netsec

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Validate checks a policy before it is saved. It returns a report together
// with a ValidationError when structural errors require rejection. Coverage
// gaps alone are reported in the Report and do not produce an error, allowing
// a service to show the gaps before rejecting or overriding a save decision.
//
// A nil or empty family table selects DefaultFamilyTable.
func Validate(policy Policy, targets []Target, family FamilyTable) (*Report, error) {
	return ValidateHooks(policy.Hooks, targets, family)
}

// ValidateHooks is the hook-slice form of Validate.
func ValidateHooks(hooks []Hook, targets []Target, family FamilyTable) (*Report, error) {
	report := Analyze(hooks, targets, family)
	if !report.Accepted {
		return report, &ValidationError{Issues: report.Errors()}
	}
	return report, nil
}

// Analyze performs the same check as Validate but never returns an error.
// Callers that prefer to inspect Report.Accepted can use this directly.
func Analyze(hooks []Hook, targets []Target, family FamilyTable) *Report {
	if len(family) == 0 {
		family = DefaultFamilyTable()
	}

	report := &Report{
		Accepted: true,
		Complete: true,
		Gaps:     make([]Gap, 0),
		Issues:   make([]Issue, 0),
	}

	addIssue := func(issue Issue) {
		report.Issues = append(report.Issues, issue)
		if issue.Severity == SeverityError {
			report.Accepted = false
		}
	}

	familyIndex, familyOrder := validateFamily(family, addIssue)
	analysedHooks := validateHooks(hooks, familyIndex, addIssue)
	normalisedTargets := validateTargets(targets, addIssue)

	prefixUsed := false
	for _, hook := range analysedHooks {
		for _, selector := range hook.selectors {
			if selector.operator == OperatorPrefix {
				prefixUsed = true
				break
			}
		}
		if prefixUsed {
			break
		}
	}
	if prefixUsed {
		addIssue(Issue{
			Code:     "prefix-normalized-only",
			Severity: SeverityWarning,
			Message: "Prefix selectors are valid only for normalized paths; relative paths, " +
				"dirfd-relative paths, and paths containing traversal segments can bypass them",
			Hook: -1,
		})
	}

	directoryTargets := make(map[string]struct{})
	for _, target := range normalisedTargets {
		if target.directory {
			directoryTargets[target.display] = struct{}{}
		}
	}
	if len(directoryTargets) > 0 {
		seen := make(map[int]map[int]struct{})
		for _, hook := range analysedHooks {
			for j, selector := range hook.selectors {
				if selector.operator != OperatorPostfix {
					continue
				}
				if seen[hook.index] == nil {
					seen[hook.index] = make(map[int]struct{})
				}
				if _, ok := seen[hook.index][j]; ok {
					continue
				}
				seen[hook.index][j] = struct{}{}
				addIssue(Issue{
					Code:     "postfix-directory-protection",
					Severity: SeverityWarning,
					Message: fmt.Sprintf("hook %d (%s) uses Postfix %q; Postfix cannot be used alone "+
						"to protect a directory", hook.index, hook.canonical, strings.Join(selector.values, ",")),
					Hook: hook.index,
					Call: hook.canonical,
				})
			}
		}
	}

	for _, target := range normalisedTargets {
		for _, call := range familyOrder {
			type uncoveredPath struct {
				index   int
				reasons []string
			}
			var uncovered []uncoveredPath

			for _, spec := range call.Paths {
				if !spec.Access.Intersects(target.access) {
					continue
				}
				covered, reasons := pathCovered(analysedHooks, call.canonical, spec, target)
				if !covered {
					uncovered = append(uncovered, uncoveredPath{index: spec.ArgIndex, reasons: reasons})
				}
			}
			if len(uncovered) == 0 {
				continue
			}

			indices := make([]int, 0, len(uncovered))
			indexSet := make(map[int]struct{}, len(uncovered))
			reasonSet := make(map[string]struct{})
			for _, item := range uncovered {
				if _, exists := indexSet[item.index]; !exists {
					indexSet[item.index] = struct{}{}
					indices = append(indices, item.index)
				}
				for _, reason := range item.reasons {
					reasonSet[reason] = struct{}{}
				}
			}
			sort.Ints(indices)
			reasons := make([]string, 0, len(reasonSet))
			for reason := range reasonSet {
				reasons = append(reasons, reason)
			}
			sort.Strings(reasons)
			if len(reasons) == 0 {
				reasons = append(reasons, fmt.Sprintf(
					"no hook on %s selects the protected path with a blocking action", call.canonical))
			}

			report.Gaps = append(report.Gaps, Gap{
				Target:    target.display,
				Directory: target.directory,
				Access:    target.access.String(),
				Call:      call.canonical,
				PathArgs:  indices,
				Reason:    strings.Join(reasons, "; "),
			})
		}
	}

	report.Complete = report.Accepted && len(report.Gaps) == 0
	return report
}

type analysedFamilyCall struct {
	canonical string
	Paths     []PathSpec
}

func validateFamily(family FamilyTable, addIssue func(Issue)) (map[string]analysedFamilyCall, []analysedFamilyCall) {
	index := make(map[string]analysedFamilyCall, len(family))
	order := make([]analysedFamilyCall, 0, len(family))

	for i, call := range family {
		canonical := CanonicalCall(call.Name)
		if canonical == "" {
			addIssue(Issue{
				Code:     "invalid-family-call",
				Severity: SeverityError,
				Message:  fmt.Sprintf("family call %d has an empty name", i),
				Hook:     -1,
			})
			continue
		}
		if _, exists := index[canonical]; exists {
			addIssue(Issue{
				Code:     "duplicate-family-call",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("duplicate family call %q; the first definition is used", canonical),
				Hook:     -1,
				Call:     canonical,
			})
			continue
		}

		valid := true
		for j, spec := range call.Paths {
			if spec.ArgIndex < 0 {
				addIssue(Issue{
					Code:     "invalid-path-argument",
					Severity: SeverityError,
					Message:  fmt.Sprintf("family call %q path spec %d has a negative argument index", canonical, j),
					Hook:     -1,
					Call:     canonical,
				})
				valid = false
			}
			if spec.Access == 0 {
				addIssue(Issue{
					Code:     "empty-path-access",
					Severity: SeverityError,
					Message:  fmt.Sprintf("family call %q path argument %d has no operations", canonical, spec.ArgIndex),
					Hook:     -1,
					Call:     canonical,
				})
				valid = false
			}
			if spec.Access&^AccessAll != 0 {
				addIssue(Issue{
					Code:     "unknown-path-access",
					Severity: SeverityError,
					Message: fmt.Sprintf("family call %q path argument %d uses unknown access bits %#x",
						canonical, spec.ArgIndex, uint32(spec.Access&^AccessAll)),
					Hook: -1,
					Call: canonical,
				})
				valid = false
			}
		}
		if len(call.Paths) == 0 {
			addIssue(Issue{
				Code:     "family-call-without-paths",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("family call %q has no path arguments and cannot affect protected paths", canonical),
				Hook:     -1,
				Call:     canonical,
			})
		}
		if !valid {
			continue
		}

		entry := analysedFamilyCall{canonical: canonical, Paths: call.Paths}
		index[canonical] = entry
		order = append(order, entry)
	}
	return index, order
}

type analysedSelector struct {
	argIndex  int
	operator  Operator
	values    []string
	pathValue []parsedPath
}

type analysedHook struct {
	index     int
	canonical string
	action    Action
	code      int
	codeOK    bool
	valid     bool
	args      map[int]string
	selectors []analysedSelector
}

func validateHooks(hooks []Hook, family map[string]analysedFamilyCall, addIssue func(Issue)) []analysedHook {
	out := make([]analysedHook, 0, len(hooks))
	for i, hook := range hooks {
		canonical := CanonicalCall(hook.Call)
		entry := analysedHook{
			index:     i,
			canonical: canonical,
			valid:     true,
			args:      make(map[int]string),
		}
		for _, arg := range hook.Args {
			entry.args[arg.Index] = arg.Type
		}

		if canonical == "" {
			addIssue(Issue{
				Code:     "invalid-hook-call",
				Severity: SeverityError,
				Message:  fmt.Sprintf("hook %d has an empty call name", i),
				Hook:     i,
			})
			entry.valid = false
		} else if _, ok := family[canonical]; !ok {
			addIssue(Issue{
				Code:     "hook-call-not-in-family-table",
				Severity: SeverityWarning,
				Message: fmt.Sprintf("hook %d calls %q, which is not in the same-family table; "+
					"it cannot cover any target in this validation", i, canonical),
				Hook: i,
				Call: canonical,
			})
		}

		action, ok := canonicalAction(hook.Action)
		if !ok {
			addIssue(Issue{
				Code:     "invalid-action",
				Severity: SeverityError,
				Message: fmt.Sprintf("hook %d (%s) uses unsupported action %q; expected Sigkill or Override",
					i, canonical, hook.Action),
				Hook: i,
				Call: canonical,
			})
			entry.valid = false
		} else {
			entry.action = action
		}

		if action == ActionOverride {
			code, hasCode := hookOverrideCode(hook)
			if !hasCode {
				addIssue(Issue{
					Code:     "override-missing-return-code",
					Severity: SeverityError,
					Message: fmt.Sprintf("hook %d (%s) uses Override without a return code; "+
						"the policy must be rejected", i, canonical),
					Hook: i,
					Call: canonical,
				})
				entry.valid = false
			} else {
				entry.code = code
				entry.codeOK = true
				if code == 0 {
					addIssue(Issue{
						Code:     "override-return-zero",
						Severity: SeverityWarning,
						Message: fmt.Sprintf("hook %d (%s) overrides with return code 0; "+
							"the operation may still succeed and is not treated as protection", i, canonical),
						Hook: i,
						Call: canonical,
					})
				}
			}
		} else if _, hasCode := hookOverrideCode(hook); hasCode {
			addIssue(Issue{
				Code:     "sigkill-return-code-ignored",
				Severity: SeverityWarning,
				Message: fmt.Sprintf("hook %d (%s) sets a return code but uses Sigkill; the return code is ignored",
					i, canonical),
				Hook: i,
				Call: canonical,
			})
		}

		for j, selector := range hook.Selectors {
			analysed := analysedSelector{argIndex: selector.ArgIndex}
			if selector.ArgIndex < 0 {
				addIssue(Issue{
					Code:     "invalid-selector-argument",
					Severity: SeverityError,
					Message:  fmt.Sprintf("hook %d selector %d has a negative argument index", i, j),
					Hook:     i,
					Call:     canonical,
				})
				entry.valid = false
				continue
			}

			operator, ok := canonicalOperator(selector.Operator)
			if !ok {
				addIssue(Issue{
					Code:     "invalid-selector-operator",
					Severity: SeverityError,
					Message: fmt.Sprintf("hook %d selector %d uses unsupported operator %q; "+
						"expected Prefix, Equal, or Postfix", i, j, selector.Operator),
					Hook: i,
					Call: canonical,
				})
				entry.valid = false
				continue
			}
			analysed.operator = operator
			analysed.values = selector.ValuesFor()
			if len(analysed.values) == 0 {
				addIssue(Issue{
					Code:     "empty-selector-value",
					Severity: SeverityError,
					Message:  fmt.Sprintf("hook %d selector %d has no values", i, j),
					Hook:     i,
					Call:     canonical,
				})
				entry.valid = false
				continue
			}

			declaredType, hasDeclaredType := entry.args[selector.ArgIndex]
			if hasDeclaredType && classifyArgType(declaredType) != argTypePath {
				addIssue(Issue{
					Code:     "non-path-argument-selector",
					Severity: SeverityWarning,
					Message: fmt.Sprintf("hook %d (%s) selects argument %d declared as %q, which is not a recognized "+
						"path string type; it cannot cover a path target", i, canonical, selector.ArgIndex, declaredType),
					Hook: i,
					Call: canonical,
				})
			}

			for _, value := range analysed.values {
				if value == "" {
					addIssue(Issue{
						Code:     "empty-selector-value",
						Severity: SeverityError,
						Message:  fmt.Sprintf("hook %d selector %d contains an empty path value", i, j),
						Hook:     i,
						Call:     canonical,
					})
					entry.valid = false
					continue
				}
				parsed := parsePathValue(value)
				analysed.pathValue = append(analysed.pathValue, parsed)

				switch operator {
				case OperatorPrefix:
					if !parsed.normalized {
						addIssue(Issue{
							Code:     "prefix-not-normalized",
							Severity: SeverityWarning,
							Message: fmt.Sprintf("hook %d (%s) Prefix value %q is not a normalized path; "+
								"Prefix selectors are valid only for normalized paths and this value is not used for coverage",
								i, canonical, value),
							Hook: i,
							Call: canonical,
						})
					}
					if !parsed.absolute {
						addIssue(Issue{
							Code:     "prefix-relative-path",
							Severity: SeverityWarning,
							Message: fmt.Sprintf("hook %d (%s) Prefix value %q is relative; "+
								"it cannot prove coverage of an absolute target", i, canonical, value),
							Hook: i,
							Call: canonical,
						})
					}
				case OperatorEqual:
					if !parsed.normalized {
						addIssue(Issue{
							Code:     "equal-not-normalized",
							Severity: SeverityWarning,
							Message: fmt.Sprintf("hook %d (%s) Equal value %q is not a normalized path; "+
								"it does not match the normalized protected target", i, canonical, value),
							Hook: i,
							Call: canonical,
						})
					}
					if !parsed.absolute {
						addIssue(Issue{
							Code:     "equal-relative-path",
							Severity: SeverityWarning,
							Message: fmt.Sprintf("hook %d (%s) Equal value %q is relative; "+
								"it cannot prove coverage of an absolute target", i, canonical, value),
							Hook: i,
							Call: canonical,
						})
					}
				}
			}
			entry.selectors = append(entry.selectors, analysed)
		}

		out = append(out, entry)
	}
	return out
}

type normalisedTarget struct {
	display   string
	path      string
	absolute  bool
	directory bool
	access    Access
}

func validateTargets(targets []Target, addIssue func(Issue)) []normalisedTarget {
	out := make([]normalisedTarget, 0, len(targets))
	for i, target := range targets {
		if strings.TrimSpace(target.Path) == "" {
			addIssue(Issue{
				Code:     "empty-target-path",
				Severity: SeverityError,
				Message:  fmt.Sprintf("target %d has an empty path", i),
				Hook:     -1,
			})
			continue
		}
		if target.Access == 0 {
			addIssue(Issue{
				Code:     "empty-target-access",
				Severity: SeverityError,
				Message:  fmt.Sprintf("target %q has no access modes", target.Path),
				Hook:     -1,
				Target:   target.Path,
			})
			continue
		}
		if target.Access&^AccessAll != 0 {
			addIssue(Issue{
				Code:     "unknown-target-access",
				Severity: SeverityError,
				Message:  fmt.Sprintf("target %q uses unknown access bits %#x", target.Path, uint32(target.Access&^AccessAll)),
				Hook:     -1,
				Target:   target.Path,
			})
			continue
		}

		cleaned := path.Clean(target.Path)
		display := target.Path
		if strings.HasSuffix(display, "/") && display != "/" {
			display = strings.TrimRight(display, "/")
		}
		directory := target.Directory || strings.HasSuffix(target.Path, "/") || cleaned == "/"

		if cleaned != target.Path && !(strings.HasSuffix(target.Path, "/") && strings.TrimRight(target.Path, "/") == cleaned) {
			addIssue(Issue{
				Code:     "target-not-normalized",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("target %q was normalized to %q for matching", target.Path, cleaned),
				Hook:     -1,
				Target:   target.Path,
			})
		}
		if !strings.HasPrefix(target.Path, "/") {
			addIssue(Issue{
				Code:     "relative-target",
				Severity: SeverityWarning,
				Message: fmt.Sprintf("target %q is relative; coverage can be proven only by matching relative selectors "+
					"and cannot account for the process working directory", target.Path),
				Hook:   -1,
				Target: target.Path,
			})
		}

		out = append(out, normalisedTarget{
			display:   display,
			path:      cleaned,
			absolute:  strings.HasPrefix(target.Path, "/"),
			directory: directory,
			access:    target.Access,
		})
	}
	return out
}

func pathCovered(hooks []analysedHook, canonical string, spec PathSpec, target normalisedTarget) (bool, []string) {
	reasons := make([]string, 0)
	reasonSet := make(map[string]struct{})
	addReason := func(reason string) {
		if reason == "" {
			return
		}
		if _, exists := reasonSet[reason]; exists {
			return
		}
		reasonSet[reason] = struct{}{}
		reasons = append(reasons, reason)
	}

	hookSeen := false
	pathSelectorSeen := false
	for _, hook := range hooks {
		if hook.canonical != canonical {
			continue
		}
		hookSeen = true
		if !hook.valid {
			addReason(fmt.Sprintf("hook %d is structurally invalid and cannot protect the target", hook.index))
			continue
		}

		declaredType, hasDeclaredType := hook.args[spec.ArgIndex]
		argIsPath := !hasDeclaredType || classifyArgType(declaredType) == argTypePath

		for _, selector := range hook.selectors {
			if selector.argIndex != spec.ArgIndex {
				continue
			}
			pathSelectorSeen = true
			if !argIsPath {
				addReason(fmt.Sprintf("hook %d selects argument %d declared as %q, not a path string",
					hook.index, spec.ArgIndex, declaredType))
				continue
			}

			for _, value := range selector.pathValue {
				matched, reason := selectorMatches(selector.operator, value, target)
				if reason != "" {
					addReason(reason)
				}
				if !matched {
					continue
				}

				raw := value.raw
				switch hook.action {
				case ActionSigkill:
					return true, nil
				case ActionOverride:
					if !hook.codeOK {
						addReason(fmt.Sprintf("hook %d is an Override without a return code", hook.index))
						continue
					}
					if hook.code == 0 {
						addReason(fmt.Sprintf("hook %d Override value %q returns 0 and may allow the operation",
							hook.index, raw))
						continue
					}
					return true, nil
				default:
					addReason(fmt.Sprintf("hook %d has no blocking action", hook.index))
				}
			}
		}
	}

	if !hookSeen {
		addReason(fmt.Sprintf("no hook is installed on %s", canonical))
	} else if !pathSelectorSeen {
		addReason(fmt.Sprintf("no hook on %s selects path argument %d", canonical, spec.ArgIndex))
	}

	if target.directory {
		for _, hook := range hooks {
			if hook.canonical != canonical || !hook.valid {
				continue
			}
			for _, selector := range hook.selectors {
				if selector.argIndex == spec.ArgIndex && selector.operator == OperatorPostfix {
					addReason(fmt.Sprintf("hook %d uses Postfix %q; Postfix cannot be used alone for directory protection",
						hook.index, strings.Join(selector.values, ",")))
				}
			}
		}
	}

	return false, reasons
}

type parsedPath struct {
	raw        string
	clean      string
	absolute   bool
	normalized bool
}

func parsePathValue(value string) parsedPath {
	return parsedPath{
		raw:        value,
		clean:      path.Clean(value),
		absolute:   strings.HasPrefix(value, "/"),
		normalized: path.Clean(value) == value,
	}
}

func selectorMatches(operator Operator, value parsedPath, target normalisedTarget) (bool, string) {
	switch operator {
	case OperatorPrefix:
		if !value.normalized {
			return false, fmt.Sprintf("Prefix value %q is not normalized; Prefix is valid only for normalized paths",
				value.raw)
		}
		if value.absolute != target.absolute {
			if target.absolute {
				return false, fmt.Sprintf("Prefix value %q is relative and cannot cover absolute target %q",
					value.raw, target.display)
			}
			return false, fmt.Sprintf("Prefix value %q is absolute and cannot cover relative target %q",
				value.raw, target.display)
		}
		return withinPath(value.clean, target.path), ""
	case OperatorEqual:
		if !value.normalized {
			return false, fmt.Sprintf("Equal value %q is not normalized; it does not match the normalized target",
				value.raw)
		}
		if value.absolute != target.absolute {
			if target.absolute {
				return false, fmt.Sprintf("Equal value %q is relative and cannot cover absolute target %q",
					value.raw, target.display)
			}
			return false, fmt.Sprintf("Equal value %q is absolute and cannot cover relative target %q",
				value.raw, target.display)
		}
		if value.clean != target.path {
			return false, ""
		}
		if target.directory {
			return false, fmt.Sprintf("Equal value %q matches only the directory itself, not its contents", value.raw)
		}
		return true, ""
	case OperatorPostfix:
		if target.directory {
			return false, fmt.Sprintf("Postfix value %q cannot be used alone to protect directory %q",
				value.raw, target.display)
		}
		if value.raw == "" {
			return false, "Postfix value is empty"
		}
		return strings.HasSuffix(target.path, value.raw), ""
	default:
		return false, fmt.Sprintf("unsupported selector operator %q", operator)
	}
}

func withinPath(prefix, target string) bool {
	if prefix == "/" {
		return strings.HasPrefix(target, "/")
	}
	if prefix == "." {
		return target != ".." && !strings.HasPrefix(target, "../")
	}
	return target == prefix || strings.HasPrefix(target, prefix+"/")
}

func hookOverrideCode(hook Hook) (int, bool) {
	if hook.ReturnCode != nil {
		return *hook.ReturnCode, true
	}
	if hook.ArgError != nil {
		return *hook.ArgError, true
	}
	return 0, false
}

func canonicalAction(action Action) (Action, bool) {
	switch strings.ToLower(strings.TrimSpace(string(action))) {
	case "sigkill", "sig_kill", "sig-kill", "kill":
		return ActionSigkill, true
	case "override":
		return ActionOverride, true
	default:
		return action, false
	}
}

func canonicalOperator(operator Operator) (Operator, bool) {
	switch strings.ToLower(strings.TrimSpace(string(operator))) {
	case "prefix":
		return OperatorPrefix, true
	case "equal":
		return OperatorEqual, true
	case "postfix", "suffix":
		return OperatorPostfix, true
	default:
		return operator, false
	}
}

// CanonicalCall strips common kernel-symbol prefixes so that sys_openat,
// __x64_sys_openat, and openat all refer to the same family entry.
func CanonicalCall(call string) string {
	call = strings.ToLower(strings.TrimSpace(call))
	prefixes := []string{
		"__arm64_compat_sys_",
		"__x64_sys_",
		"__ia32_sys_",
		"__arm64_sys_",
		"__s390x_sys_",
		"__riscv_sys_",
		"__do_sys_",
		"__se_sys_",
		"do_sys_",
		"do_",
		"ksys_",
		"__sys_",
		"sys_",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(call, prefix) {
			call = strings.TrimPrefix(call, prefix)
			break
		}
	}
	return call
}

type argTypeClass int

const (
	argTypeUnknown argTypeClass = iota
	argTypePath
	argTypeNonPath
)

func classifyArgType(raw string) argTypeClass {
	t := strings.ToLower(strings.TrimSpace(raw))
	t = strings.ReplaceAll(t, "const ", "")
	t = strings.ReplaceAll(t, "volatile ", "")
	t = strings.ReplaceAll(t, "__user ", "")
	t = strings.ReplaceAll(t, "__kernel ", "")
	t = strings.ReplaceAll(t, " ", "")
	t = strings.TrimSuffix(t, "*")

	switch t {
	case "string", "char", "char[]", "cstring", "path", "pathname", "filename", "filepath":
		return argTypePath
	case "int", "int32", "int64", "uint", "uint32", "uint64", "u8", "u16", "u32", "u64",
		"s8", "s16", "s32", "s64", "long", "ulong", "short", "ushort", "bool",
		"fd", "flags", "mode", "pid", "size", "bytes", "sockaddr", "struct":
		return argTypeNonPath
	default:
		return argTypeUnknown
	}
}
