package netsec

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const allSubjects = "ALL"

var aliasNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Rule is one fully expanded command rule.
//
// Program is compared literally after validation of its absolute-path form.
// AnyArgs selects the arbitrary-argument form; the traversal and option
// safeguards still apply. When AnyArgs is false, an empty ArgPattern means the
// command takes no arguments; a non-empty ArgPattern is matched with fnmatch
// semantics.
type Rule struct {
	User       string
	Host       string
	RunAs      string
	Program    string
	ArgPattern string
	AnyArgs    bool
	Negated    bool
}

// Policy is an ordered list of rules. Later matching rules override earlier
// matching rules for the same command.
type Policy struct {
	Rules []Rule
}

// Request is one authorization request submitted to the gateway.
type Request struct {
	User    string
	Host    string
	RunAs   string
	Program string
	Args    []string
}

// Decision is the result of evaluating a Policy.
type Decision struct {
	Allowed   bool
	Matched   bool
	RuleIndex int
	Rule      Rule
	Reason    string
}

const (
	ReasonNoMatch = "no matching rule"
	ReasonAllow   = "last matching rule allows the request"
	ReasonDeny    = "last matching rule denies the request"
)

// Parse parses sudoers-style Cmnd_Alias declarations and rules, expands every
// alias, and returns a validated Policy.
func Parse(source string) (*Policy, error) {
	parser := policyParser{
		aliases: make(map[string][]commandItem),
	}

	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	for index, rawLine := range lines {
		line, err := stripComment(rawLine)
		if err != nil {
			return nil, lineError(index+1, err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if isAliasDeclaration(line) {
			if err := parser.parseAlias(line); err != nil {
				return nil, lineError(index+1, err)
			}
			continue
		}

		items, rule, err := parseRuleLine(line)
		if err != nil {
			return nil, lineError(index+1, err)
		}
		for _, item := range items {
			parser.pending = append(parser.pending, pendingRule{
				user:  rule.User,
				host:  rule.Host,
				runAs: rule.RunAs,
				item:  item,
			})
		}
	}

	if err := parser.validateAliases(); err != nil {
		return nil, err
	}
	policy, err := parser.expand()
	if err != nil {
		return nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return policy, nil
}

func (parser *policyParser) validateAliases() error {
	names := make([]string, 0, len(parser.aliases))
	for name := range parser.aliases {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if _, err := parser.expandItem(commandItem{alias: name}, nil); err != nil {
			return err
		}
	}
	return nil
}

// Compile is an alias for Parse.
func Compile(source string) (*Policy, error) {
	return Parse(source)
}

// Authorize parses source and evaluates request in one step. Callers that
// authorize more than one request should parse once and reuse the Policy.
func Authorize(source string, request Request) (Decision, error) {
	policy, err := Parse(source)
	if err != nil {
		return Decision{}, err
	}
	return policy.Authorize(request), nil
}

// NewPolicy validates and returns a policy assembled from already parsed
// rules. Rule order is preserved.
func NewPolicy(rules []Rule) (*Policy, error) {
	policy := &Policy{Rules: append([]Rule(nil), rules...)}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return policy, nil
}

// Validate checks the structural invariants required by the matcher.
func (policy *Policy) Validate() error {
	if policy == nil {
		return fmt.Errorf("nil policy")
	}
	for index, rule := range policy.Rules {
		prefix := fmt.Sprintf("rule %d", index+1)
		if rule.User == "" || rule.Host == "" || rule.RunAs == "" {
			return fmt.Errorf("%s: user, host, and run-as user are required", prefix)
		}
		if err := validateProgram(rule.Program); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
		if rule.AnyArgs && rule.ArgPattern != "" {
			return fmt.Errorf("%s: AnyArgs rule must not also have an argument pattern", prefix)
		}
	}
	return nil
}

// Authorize evaluates request. It selects the last matching rule (including a
// negated rule) for the requested program; all earlier matches are overridden.
func (policy *Policy) Authorize(request Request) Decision {
	decision := Decision{RuleIndex: -1, Reason: ReasonNoMatch}
	if policy == nil {
		return decision
	}

	for index := range policy.Rules {
		rule := policy.Rules[index]
		if !subjectMatches(rule.User, request.User) ||
			!subjectMatches(rule.Host, request.Host) ||
			!subjectMatches(rule.RunAs, request.RunAs) {
			continue
		}
		if rule.Program != request.Program {
			continue
		}
		if !argumentsMatch(rule, request.Args) {
			continue
		}

		decision.Matched = true
		decision.RuleIndex = index
		decision.Rule = rule
		decision.Allowed = !rule.Negated
		if rule.Negated {
			decision.Reason = ReasonDeny
		} else {
			decision.Reason = ReasonAllow
		}
	}
	return decision
}

// Allowed is a convenience wrapper around Authorize.
func (policy *Policy) Allowed(request Request) bool {
	return policy.Authorize(request).Allowed
}

func subjectMatches(ruleValue, requestValue string) bool {
	return ruleValue == allSubjects || ruleValue == requestValue
}

func argumentsMatch(rule Rule, arguments []string) bool {
	joined := strings.Join(arguments, " ")

	if rule.AnyArgs {
		if rule.Negated {
			return true
		}
		if hasParentDirectoryArgument(arguments) {
			return false
		}
		return allOptionsLiterallyPresent(rule, arguments)
	}

	if rule.ArgPattern == "" {
		return len(arguments) == 0
	}

	if !fnmatch(rule.ArgPattern, joined) {
		return false
	}
	if rule.Negated {
		return true
	}
	if patternHasWildcard(rule.ArgPattern) && hasParentDirectoryArgument(arguments) {
		return false
	}
	return allOptionsLiterallyPresent(rule, arguments)
}

func hasParentDirectoryArgument(arguments []string) bool {
	for _, argument := range arguments {
		if hasParentDirectorySegment(argument) {
			return true
		}
	}
	return false
}

func allOptionsLiterallyPresent(rule Rule, arguments []string) bool {
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, "-") {
			continue
		}
		if rule.AnyArgs || rule.ArgPattern == "" ||
			!patternContainsLiteralOption(rule.ArgPattern, argument) {
			return false
		}
	}
	return true
}

type commandItem struct {
	alias      string
	program    string
	argPattern string
	anyArgs    bool
	negated    bool
}

type pendingRule struct {
	user  string
	host  string
	runAs string
	item  commandItem
}

type policyParser struct {
	aliases map[string][]commandItem
	pending []pendingRule
}

func (parser *policyParser) parseAlias(line string) error {
	remainder := strings.TrimSpace(strings.TrimPrefix(line, "Cmnd_Alias"))
	equals := findTopLevelRune(remainder, '=')
	if equals < 0 {
		return fmt.Errorf("Cmnd_Alias declaration is missing '='")
	}

	name := strings.TrimSpace(remainder[:equals])
	if !aliasNamePattern.MatchString(name) {
		return fmt.Errorf("invalid Cmnd_Alias name %q", name)
	}
	if _, exists := parser.aliases[name]; exists {
		return fmt.Errorf("Cmnd_Alias %s is declared more than once", name)
	}

	rawItems, err := splitTopLevel(remainder[equals+1:], ',')
	if err != nil {
		return err
	}

	items := make([]commandItem, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, err := parseCommandItem(rawItem)
		if err != nil {
			return fmt.Errorf("Cmnd_Alias %s: %w", name, err)
		}
		items = append(items, item)
	}
	parser.aliases[name] = items
	return nil
}

func (parser *policyParser) expand() (*Policy, error) {
	policy := &Policy{}
	for _, pending := range parser.pending {
		items, err := parser.expandItem(pending.item, nil)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			policy.Rules = append(policy.Rules, Rule{
				User:       pending.user,
				Host:       pending.host,
				RunAs:      pending.runAs,
				Program:    item.program,
				ArgPattern: item.argPattern,
				AnyArgs:    item.anyArgs,
				Negated:    item.negated,
			})
		}
	}
	return policy, nil
}

func (parser *policyParser) expandItem(item commandItem, stack []string) ([]commandItem, error) {
	if item.alias == "" {
		return []commandItem{item}, nil
	}

	for _, name := range stack {
		if name == item.alias {
			return nil, fmt.Errorf("Cmnd_Alias cycle: %s", strings.Join(append(stack, item.alias), " -> "))
		}
	}
	definitions, exists := parser.aliases[item.alias]
	if !exists {
		return nil, fmt.Errorf("unknown Cmnd_Alias %s", item.alias)
	}

	var expanded []commandItem
	nextStack := append(append([]string(nil), stack...), item.alias)
	for _, definition := range definitions {
		items, err := parser.expandItem(definition, nextStack)
		if err != nil {
			return nil, err
		}
		for _, expandedItem := range items {
			expandedItem.negated = expandedItem.negated != item.negated
			expanded = append(expanded, expandedItem)
		}
	}
	return expanded, nil
}

func parseRuleLine(line string) ([]commandItem, Rule, error) {
	var rule Rule
	equals := findTopLevelRune(line, '=')
	if equals < 0 {
		return nil, rule, fmt.Errorf("rule is missing '='")
	}

	subjects := strings.Fields(strings.TrimSpace(line[:equals]))
	if len(subjects) != 2 {
		return nil, rule, fmt.Errorf("rule must have the form user host=(run-as) command")
	}
	rule.User, rule.Host = subjects[0], subjects[1]

	remainder := strings.TrimSpace(line[equals+1:])
	if !strings.HasPrefix(remainder, "(") {
		return nil, rule, fmt.Errorf("rule is missing the run-as user in parentheses")
	}
	closeParen := strings.IndexRune(remainder, ')')
	if closeParen < 0 {
		return nil, rule, fmt.Errorf("rule has an unterminated run-as user")
	}
	rule.RunAs = strings.TrimSpace(remainder[1:closeParen])
	if rule.RunAs == "" {
		return nil, rule, fmt.Errorf("rule has an empty run-as user")
	}
	if strings.IndexFunc(rule.RunAs, unicode.IsSpace) >= 0 {
		return nil, rule, fmt.Errorf("run-as user must not contain whitespace")
	}

	commands := strings.TrimSpace(remainder[closeParen+1:])
	if commands == "" {
		return nil, rule, fmt.Errorf("rule has no command")
	}
	rawItems, err := splitTopLevel(commands, ',')
	if err != nil {
		return nil, rule, err
	}

	items := make([]commandItem, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, err := parseCommandItem(rawItem)
		if err != nil {
			return nil, rule, err
		}
		items = append(items, item)
	}
	return items, rule, nil
}

func parseCommandItem(raw string) (commandItem, error) {
	var item commandItem
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return item, fmt.Errorf("empty command")
	}
	if strings.HasPrefix(raw, "!") {
		item.negated = true
		raw = strings.TrimSpace(raw[1:])
		if raw == "" {
			return item, fmt.Errorf("negated command is empty")
		}
	}

	words, err := lexWords(raw)
	if err != nil {
		return item, err
	}
	if len(words) == 0 {
		return item, fmt.Errorf("empty command")
	}

	programOrAlias := unescapeLiteral(words[0].text)
	if !path.IsAbs(programOrAlias) {
		if strings.Contains(programOrAlias, "/") {
			return item, fmt.Errorf("program %q is not an absolute path", programOrAlias)
		}
		if !aliasNamePattern.MatchString(programOrAlias) {
			return item, fmt.Errorf("program %q is neither an absolute path nor a Cmnd_Alias", programOrAlias)
		}
		if len(words) != 1 {
			return item, fmt.Errorf("Cmnd_Alias %s must not have an argument pattern", programOrAlias)
		}
		item.alias = programOrAlias
		return item, nil
	}

	if err := validateProgram(programOrAlias); err != nil {
		return item, err
	}
	item.program = programOrAlias

	if len(words) == 1 {
		item.anyArgs = true
		return item, nil
	}
	if len(words) == 2 && words[1].quoted && words[1].text == "" {
		return item, nil
	}

	values := make([]string, 0, len(words)-1)
	for _, word := range words[1:] {
		values = append(values, word.text)
	}
	item.argPattern = strings.Join(values, " ")
	return item, nil
}

func unescapeLiteral(value string) string {
	var output strings.Builder
	escaped := false
	for _, char := range value {
		if escaped {
			output.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		output.WriteRune(char)
	}
	if escaped {
		output.WriteRune('\\')
	}
	return output.String()
}

func validateProgram(program string) error {
	if !path.IsAbs(program) {
		return fmt.Errorf("program %q must be an absolute path", program)
	}
	if strings.ContainsAny(program, "*?[") {
		return fmt.Errorf("program %q must not contain wildcard characters", program)
	}
	if strings.IndexFunc(program, func(r rune) bool { return r == 0 || r == '\n' || r == '\r' }) >= 0 {
		return fmt.Errorf("program %q contains a control character", program)
	}
	return nil
}

func isAliasDeclaration(line string) bool {
	if !strings.HasPrefix(line, "Cmnd_Alias") {
		return false
	}
	return len(line) == len("Cmnd_Alias") || unicode.IsSpace(rune(line[len("Cmnd_Alias")]))
}

type word struct {
	text   string
	quoted bool
}

func lexWords(input string) ([]word, error) {
	var words []word
	var current strings.Builder
	quoted := false
	hadQuote := false
	inClass := false
	escaped := false

	flush := func() {
		if current.Len() == 0 && !hadQuote {
			return
		}
		words = append(words, word{text: current.String(), quoted: hadQuote})
		current.Reset()
		hadQuote = false
	}

	for _, char := range input {
		if escaped {
			current.WriteRune('\\')
			current.WriteRune(char)
			escaped = false
			continue
		}

		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			hadQuote = true
			continue
		}
		if !quoted {
			if char == '[' {
				inClass = true
			} else if char == ']' && inClass {
				inClass = false
			}
			if unicode.IsSpace(char) && !inClass {
				flush()
				continue
			}
		}
		current.WriteRune(char)
	}
	if escaped {
		return nil, fmt.Errorf("trailing escape in command")
	}
	if quoted {
		return nil, fmt.Errorf("unterminated quote in command")
	}
	if inClass {
		return nil, fmt.Errorf("unterminated character class in command")
	}
	flush()
	return words, nil
}

func splitTopLevel(input string, separator rune) ([]string, error) {
	var fields []string
	var current strings.Builder
	quoted := false
	inClass := false
	escaped := false

	for _, char := range input {
		if escaped {
			current.WriteRune('\\')
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			current.WriteRune(char)
			continue
		}
		if !quoted {
			if char == '[' {
				inClass = true
			} else if char == ']' && inClass {
				inClass = false
			}
			if char == separator && !inClass {
				fields = append(fields, strings.TrimSpace(current.String()))
				current.Reset()
				continue
			}
		}
		current.WriteRune(char)
	}
	if escaped {
		return nil, fmt.Errorf("trailing escape")
	}
	if quoted {
		return nil, fmt.Errorf("unterminated quote")
	}
	if inClass {
		return nil, fmt.Errorf("unterminated character class")
	}
	fields = append(fields, strings.TrimSpace(current.String()))
	return fields, nil
}

func stripComment(line string) (string, error) {
	var output strings.Builder
	quoted := false
	inClass := false
	escaped := false

	for _, char := range line {
		if escaped {
			output.WriteRune('\\')
			output.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			output.WriteRune(char)
			continue
		}
		if !quoted {
			if char == '[' {
				inClass = true
			} else if char == ']' && inClass {
				inClass = false
			}
			if char == '#' && !inClass {
				break
			}
		}
		output.WriteRune(char)
	}
	if escaped {
		return "", fmt.Errorf("trailing escape")
	}
	if quoted {
		return "", fmt.Errorf("unterminated quote")
	}
	if inClass {
		return "", fmt.Errorf("unterminated character class")
	}
	return output.String(), nil
}

func findTopLevelRune(input string, wanted rune) int {
	quoted := false
	inClass := false
	escaped := false
	for index, char := range input {
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			continue
		}
		if !quoted {
			if char == '[' {
				inClass = true
				continue
			}
			if char == ']' && inClass {
				inClass = false
				continue
			}
			if char == wanted && !inClass {
				return index
			}
		}
	}
	return -1
}

func lineError(line int, err error) error {
	return fmt.Errorf("line %d: %w", line, err)
}
