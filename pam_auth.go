package netsec

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"
)

type ControlKind int

const (
	// Required marks a failed module as terminal failure but continues execution.
	Required ControlKind = iota + 1
	// Requisite stops the stack immediately when the module fails.
	Requisite
	// Sufficient returns success immediately if no required module previously failed.
	Sufficient
	// Optional contributes only when it is the sole module in the stack.
	Optional
	// Explicit selects actions from a bracketed action table.
	Explicit
)

type ActionKind int

const (
	// IgnoreAction leaves the accumulated stack result unchanged.
	IgnoreAction ActionKind = iota + 1
	// OKAction records module success and continues the stack.
	OKAction
	// BadAction records stack failure and continues the stack.
	BadAction
	// DieAction records stack failure and stops the stack.
	DieAction
	// DoneAction returns success immediately when prior required modules did not fail.
	DoneAction
	// ResetAction clears the accumulated stack result.
	ResetAction
	// JumpAction skips later modules without changing the accumulated result.
	JumpAction
)

type Outcome int

const (
	// ModuleSuccess is the enumerated value for a successful module invocation.
	ModuleSuccess Outcome = iota + 1
	// ModuleFailure is the enumerated value for a failed module invocation.
	ModuleFailure
	// ModuleSkipped marks a module that control flow never reached.
	ModuleSkipped
)

type Decision int

const (
	// Undecided means evaluation has not reached an explicit pass or deny yet.
	Undecided Decision = iota
	// AuthPass means the auth stack accepts the authentication.
	AuthPass
	// AuthDeny means the auth stack rejects the authentication.
	AuthDeny
)

// Action describes how a module result changes stack state or control flow.
type Action struct {
	Kind ActionKind
	Skip int
}

// Control is either a built-in PAM control flag or an explicit action table.
type Control struct {
	Kind    ControlKind
	Actions map[string]Action
}

// Rule is one parsed auth entry, excluding any non-auth management type.
type Rule struct {
	Type    string
	Control Control
	Module  string
	Args    []string
}

// Path records one enumerated success/failure assignment and its evaluated result.
type Path struct {
	Outcomes []Outcome
	Reached  []bool
	Decision Decision
}

// ParseAuthConfig parses auth rules from a PAM configuration string.
func ParseAuthConfig(input string) ([]Rule, error) {
	return ParseAuthStack(strings.NewReader(input))
}

// ParseAuthStack parses auth rules from a PAM configuration stream.
func ParseAuthStack(reader io.Reader) ([]Rule, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	rules := make([]Rule, 0, len(lines))
	for lineNumber, line := range lines {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}

		rule, err := ParseRule(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber+1, err)
		}
		if rule.Type != "auth" {
			continue
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// ParseRule parses a single PAM configuration line.
func ParseRule(line string) (Rule, error) {
	fields, err := splitPAMFields(line)
	if err != nil {
		return Rule{}, err
	}
	if len(fields) < 3 {
		return Rule{}, fmt.Errorf("expected rule type, control and module")
	}

	ruleType := strings.ToLower(fields[0])
	if ruleType != "auth" {
		return Rule{Type: ruleType}, nil
	}

	control, err := ParseControl(fields[1])
	if err != nil {
		return Rule{}, err
	}

	return Rule{
		Type:    ruleType,
		Control: control,
		Module:  fields[2],
		Args:    append([]string(nil), fields[3:]...),
	}, nil
}

// ParseControl parses a built-in control flag or a bracketed action table.
func ParseControl(value string) (Control, error) {
	lowered := strings.ToLower(value)
	switch lowered {
	case "required":
		return Control{Kind: Required}, nil
	case "requisite":
		return Control{Kind: Requisite}, nil
	case "sufficient":
		return Control{Kind: Sufficient}, nil
	case "optional":
		return Control{Kind: Optional}, nil
	}

	if len(value) < 2 || value[0] != '[' || value[len(value)-1] != ']' {
		return Control{}, fmt.Errorf("unsupported control %q", value)
	}

	control := Control{Kind: Explicit, Actions: make(map[string]Action)}
	for _, part := range strings.Fields(value[1 : len(value)-1]) {
		name, rawAction, found := strings.Cut(part, "=")
		if !found || name == "" {
			return Control{}, fmt.Errorf("invalid explicit action %q", part)
		}

		action, err := parseAction(rawAction)
		if err != nil {
			return Control{}, fmt.Errorf("control %s: %w", name, err)
		}
		control.Actions[strings.ToLower(name)] = action
	}
	if len(control.Actions) == 0 {
		return Control{}, fmt.Errorf("explicit control is empty")
	}
	return control, nil
}

func parseAction(value string) (Action, error) {
	switch strings.ToLower(value) {
	case "ignore":
		return Action{Kind: IgnoreAction}, nil
	case "ok":
		return Action{Kind: OKAction}, nil
	case "bad":
		return Action{Kind: BadAction}, nil
	case "die":
		return Action{Kind: DieAction}, nil
	case "done":
		return Action{Kind: DoneAction}, nil
	case "reset":
		return Action{Kind: ResetAction}, nil
	}

	skip := 0
	for _, char := range value {
		if !unicode.IsDigit(char) {
			return Action{}, fmt.Errorf("unsupported action %q", value)
		}
		skip = skip*10 + int(char-'0')
	}
	if skip <= 0 {
		return Action{}, fmt.Errorf("skip action must be positive")
	}
	return Action{Kind: JumpAction, Skip: skip}, nil
}

// EvaluateAuthStack applies one success/failure value per auth rule.
func EvaluateAuthStack(rules []Rule, outcomes []Outcome) (Path, error) {
	if len(outcomes) != len(rules) {
		return Path{}, fmt.Errorf("got %d outcomes for %d rules", len(outcomes), len(rules))
	}

	path := Path{
		Outcomes: append([]Outcome(nil), outcomes...),
		Reached:  make([]bool, len(rules)),
		Decision: Undecided,
	}

	requiredFailed := false
	hasSuccessfulControl := false
	optionalSuccesses := 0

	for index := 0; index < len(rules); index++ {
		outcome := outcomes[index]
		if outcome != ModuleSuccess && outcome != ModuleFailure {
			return Path{}, fmt.Errorf("rule %d has invalid enumerated outcome", index)
		}

		path.Reached[index] = true
		action, err := actionFor(rules[index].Control, outcome)
		if err != nil {
			return Path{}, err
		}

		if rules[index].Control.Kind == Optional && outcome == ModuleSuccess {
			optionalSuccesses++
		}

		switch action.Kind {
		case IgnoreAction:
		case OKAction:
			hasSuccessfulControl = true
		case BadAction:
			requiredFailed = true
		case DieAction:
			requiredFailed = true
			path.Decision = AuthDeny
			return path, nil
		case DoneAction:
			if requiredFailed {
				continue
			}
			path.Decision = AuthPass
			return path, nil
		case ResetAction:
			requiredFailed = false
			hasSuccessfulControl = false
			optionalSuccesses = 0
		case JumpAction:
			for skipped := 1; skipped <= action.Skip && index+skipped < len(rules); skipped++ {
				path.Reached[index+skipped] = false
			}
			index += action.Skip
		default:
			return Path{}, fmt.Errorf("rule %d uses unsupported action", index)
		}
	}

	if requiredFailed {
		path.Decision = AuthDeny
		return path, nil
	}
	if hasSuccessfulControl {
		path.Decision = AuthPass
		return path, nil
	}
	if len(rules) == 1 && rules[0].Control.Kind == Optional && optionalSuccesses == 1 {
		path.Decision = AuthPass
	} else {
		path.Decision = AuthDeny
	}
	return path, nil
}

// EnumerateAuthPaths evaluates every binary success/failure combination.
func EnumerateAuthPaths(rules []Rule) []Path {
	var paths []Path
	outcomes := make([]Outcome, len(rules))

	var enumerate func(index int)
	enumerate = func(index int) {
		if index == len(rules) {
			path, err := EvaluateAuthStack(rules, outcomes)
			if err == nil {
				paths = append(paths, path)
			}
			return
		}

		outcomes[index] = ModuleSuccess
		enumerate(index + 1)
		outcomes[index] = ModuleFailure
		enumerate(index + 1)
	}
	enumerate(0)
	return paths
}

// PasswordBypassPaths returns accepting paths where a password module fails.
func PasswordBypassPaths(rules []Rule) []Path {
	return BypassPaths(rules, Rule.IsPasswordModule)
}

// BypassPaths returns accepting paths where any sensitive rule is assigned failure.
func BypassPaths(rules []Rule, sensitive func(Rule) bool) []Path {
	if sensitive == nil {
		return nil
	}

	sensitiveIndexes := make(map[int]bool)
	for index, rule := range rules {
		if sensitive(rule) {
			sensitiveIndexes[index] = true
		}
	}
	if len(sensitiveIndexes) == 0 {
		return nil
	}

	var bypasses []Path
	for _, path := range EnumerateAuthPaths(rules) {
		if path.Decision != AuthPass {
			continue
		}
		for index := range sensitiveIndexes {
			if path.Outcomes[index] == ModuleFailure {
				bypasses = append(bypasses, path)
				break
			}
		}
	}
	return bypasses
}

// IsPasswordModule reports whether the rule uses pam_unix for password checking.
func (rule Rule) IsPasswordModule() bool {
	return filepath.Base(rule.Module) == "pam_unix.so"
}

// SkippedModules returns indexes never reached because of a control-flow jump.
func (path Path) SkippedModules() []int {
	var skipped []int
	for index, reached := range path.Reached {
		if !reached {
			skipped = append(skipped, index)
		}
	}
	return skipped
}

// ActualOutcomes returns enumerated outcomes with skipped modules marked accordingly.
func (path Path) ActualOutcomes() []Outcome {
	actual := append([]Outcome(nil), path.Outcomes...)
	for index, reached := range path.Reached {
		if !reached {
			actual[index] = ModuleSkipped
		}
	}
	return actual
}

// String renders the final auth decision.
func (decision Decision) String() string {
	switch decision {
	case AuthPass:
		return "pass"
	case AuthDeny:
		return "deny"
	default:
		return "undecided"
	}
}

// String renders a module outcome.
func (outcome Outcome) String() string {
	switch outcome {
	case ModuleSuccess:
		return "success"
	case ModuleFailure:
		return "failure"
	case ModuleSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

func actionFor(control Control, outcome Outcome) (Action, error) {
	if control.Kind != Explicit {
		return simpleAction(control.Kind, outcome), nil
	}

	if outcome == ModuleSuccess {
		if action, found := control.Actions["success"]; found {
			return action, nil
		}
	}
	if action, found := control.Actions["default"]; found {
		return action, nil
	}
	return Action{Kind: BadAction}, nil
}

func simpleAction(kind ControlKind, outcome Outcome) Action {
	switch kind {
	case Required:
		if outcome == ModuleSuccess {
			return Action{Kind: OKAction}
		}
		return Action{Kind: BadAction}
	case Requisite:
		if outcome == ModuleSuccess {
			return Action{Kind: OKAction}
		}
		return Action{Kind: DieAction}
	case Sufficient:
		if outcome == ModuleSuccess {
			return Action{Kind: DoneAction}
		}
		return Action{Kind: IgnoreAction}
	default:
		return Action{Kind: IgnoreAction}
	}
}

func splitPAMFields(line string) ([]string, error) {
	fields := make([]string, 0)
	var builder strings.Builder
	inBrackets := false

	for _, char := range line {
		switch {
		case char == '[':
			if inBrackets {
				return nil, fmt.Errorf("nested control brackets are not allowed")
			}
			inBrackets = true
			builder.WriteRune(char)
		case char == ']':
			if !inBrackets {
				return nil, fmt.Errorf("unmatched control bracket")
			}
			inBrackets = false
			builder.WriteRune(char)
		case unicode.IsSpace(char) && !inBrackets:
			if builder.Len() > 0 {
				fields = append(fields, builder.String())
				builder.Reset()
			}
		default:
			builder.WriteRune(char)
		}
	}
	if inBrackets {
		return nil, fmt.Errorf("unmatched control bracket")
	}
	if builder.Len() > 0 {
		fields = append(fields, builder.String())
	}
	return fields, nil
}
