package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Reasons returned by Validate and Analyze. Use errors.Is to test them.
var (
	// ErrBackreferenceToOpenGroup reports a reference to a group that is
	// still open at the point of the reference. This includes a group that
	// refers to itself and a group that refers to one of its open ancestors.
	ErrBackreferenceToOpenGroup = errors.New("backreference targets a group that has not closed")

	// ErrBackreferenceToFutureGroup reports a reference to a group that opens
	// later in the pattern. Such a group has not ended yet either.
	ErrBackreferenceToFutureGroup = errors.New("backreference targets a group that opens later")

	// ErrUnknownGroup reports a named reference that does not resolve.
	ErrUnknownGroup = errors.New("backreference targets an undefined group")

	// ErrUnbalancedGroups reports a group or comment that is not closed.
	ErrUnbalancedGroups = errors.New("unbalanced group")
)

// Group describes a capturing group found in a pattern.
type Group struct {
	// Number is the one-based capturing-group number.
	Number int
	// Name is empty for unnamed groups.
	Name string
	// Start is the byte offset of the opening parenthesis.
	Start int
	// End is the byte offset just after the closing parenthesis.
	End int
}

// Backreference describes one backreference found in a pattern.
type Backreference struct {
	// Offset is the byte offset where the reference starts.
	Offset int
	// Token is the source text of the reference, such as `\1` or `\k<name>`.
	Token string
	// Target is the one-based number of the referenced group.
	Target int
	// TargetName is set for named references.
	TargetName string
	// EnclosingGroup is the innermost capturing group that contains the
	// reference, or zero when the reference is at the top level.
	EnclosingGroup int
	// TargetClosed reports whether the target group had already ended when
	// the reference was encountered.
	TargetClosed bool
}

// Report is the result of analyzing a pattern.
type Report struct {
	Pattern        string
	Groups         []Group
	Backreferences []Backreference
	// Safe is true when every resolved backreference targets a group that
	// has already closed.
	Safe bool
}

// ValidationError describes a rejected pattern and the location of the
// offending construct.
type ValidationError struct {
	Pattern        string
	Offset         int
	Line           int
	Column         int
	Target         int
	TargetName     string
	EnclosingGroup int
	Reason         error
	Detail         string
}

// BackreferenceError is kept as an alias for callers that want a name that
// emphasizes backreference failures.
type BackreferenceError = ValidationError

// Error implements error.
func (e *ValidationError) Error() string {
	location := fmt.Sprintf("offset %d (line %d, column %d)", e.Offset, e.Line, e.Column)
	message := "netsec: "
	if e.Detail != "" {
		message += e.Detail + " at " + location
	} else if e.TargetName != "" {
		message += fmt.Sprintf("backreference to group %q at %s", e.TargetName, location)
	} else if e.Target > 0 {
		message += fmt.Sprintf("backreference to group %d at %s", e.Target, location)
	} else {
		message += "invalid pattern at " + location
	}
	if e.Reason != nil {
		message += ": " + e.Reason.Error()
	}
	return message
}

// Unwrap exposes the sentinel reason to errors.Is and errors.As.
func (e *ValidationError) Unwrap() error {
	return e.Reason
}

// Validate checks a regular-expression pattern and rejects backreferences
// that target a group which has not yet closed. A reference to a group that
// closed earlier is allowed, including when the reference is inside another
// group that is still open.
func Validate(pattern string) error {
	_, err := Analyze(pattern)
	return err
}

// ValidatePattern is an alias for Validate.
func ValidatePattern(pattern string) error {
	return Validate(pattern)
}

// ValidateRegexp is an alias for Validate.
func ValidateRegexp(pattern string) error {
	return Validate(pattern)
}

// ValidateRegex is an alias for Validate.
func ValidateRegex(pattern string) error {
	return Validate(pattern)
}

// ValidateBackreferences is an alias for Validate.
func ValidateBackreferences(pattern string) error {
	return Validate(pattern)
}

// ValidateBackrefs is an alias for Validate.
func ValidateBackrefs(pattern string) error {
	return Validate(pattern)
}

// Check is an alias for Validate.
func Check(pattern string) error {
	return Validate(pattern)
}

// CheckPattern is an alias for Validate.
func CheckPattern(pattern string) error {
	return Validate(pattern)
}

// IsSafe reports whether Validate accepts the pattern.
func IsSafe(pattern string) bool {
	return Validate(pattern) == nil
}

// IsValid reports whether Validate accepts the pattern.
func IsValid(pattern string) bool {
	return Validate(pattern) == nil
}

// Analyze parses a pattern, records its capturing groups and backreferences,
// and validates each reference against the group's lifetime.
//
// The returned report is populated even when a backreference is rejected.
func Analyze(pattern string) (*Report, error) {
	parsed, err := parsePattern(pattern)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Pattern: pattern,
		Groups:  append([]Group(nil), parsed.groups...),
	}
	var firstErr error
	for _, raw := range parsed.refs {
		reference, resolved, err := parsed.resolve(raw)
		if !resolved {
			continue
		}
		report.Backreferences = append(report.Backreferences, reference)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	report.Safe = firstErr == nil
	return report, firstErr
}

type frame struct {
	number    int
	start     int
	capturing bool
}

type rawReference struct {
	start      int
	end        int
	number     int
	numberText string
	name       string
	numeric    bool
	enclosing  int
}

type parsedPattern struct {
	pattern  string
	groups   []Group
	refs     []rawReference
	byNumber map[int]int
	byName   map[string][]int
}

func parsePattern(pattern string) (*parsedPattern, error) {
	parsed := &parsedPattern{
		pattern:  pattern,
		byNumber: make(map[int]int),
		byName:   make(map[string][]int),
	}

	var stack []frame
	nextGroup := 1
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '\\':
			next, err := parsed.scanEscape(i, stack)
			if err != nil {
				return nil, err
			}
			i = next

		case '[':
			i = skipCharacterClass(pattern, i)

		case '(':
			if strings.HasPrefix(pattern[i:], "(?#") {
				next, ok := skipComment(pattern, i+3)
				if !ok {
					return nil, parsed.syntaxError(i, ErrUnbalancedGroups, "unterminated comment")
				}
				i = next
				continue
			}

			if strings.HasPrefix(pattern[i:], "(?P=") {
				name, next, ok := readDelimited(pattern, i+3, ')')
				if !ok {
					return nil, parsed.syntaxError(i, ErrUnbalancedGroups, "unterminated named backreference")
				}
				parsed.refs = append(parsed.refs, rawReference{
					start:     i,
					end:       next,
					name:      name,
					enclosing: innermostGroup(stack),
				})
				i = next
				continue
			}

			if name, next, ok := namedGroupName(pattern, i); ok {
				number := nextGroup
				nextGroup++

				index := len(parsed.groups)
				parsed.groups = append(parsed.groups, Group{
					Number: number,
					Name:   name,
					Start:  i,
					End:    -1,
				})
				parsed.byNumber[number] = index
				parsed.byName[name] = append(parsed.byName[name], index)
				stack = append(stack, frame{number: number, start: i, capturing: true})
				i = next
				continue
			}

			if strings.HasPrefix(pattern[i:], "(?(") {
				next, ok := skipGroupExpression(pattern, i+2)
				if !ok {
					return nil, parsed.syntaxError(i, ErrUnbalancedGroups, "unterminated conditional group")
				}
				stack = append(stack, frame{start: i})
				i = next
				continue
			}

			if i+1 < len(pattern) && pattern[i+1] == '?' {
				stack = append(stack, frame{start: i})
				i += 2
				continue
			}

			number := nextGroup
			nextGroup++

			index := len(parsed.groups)
			parsed.groups = append(parsed.groups, Group{
				Number: number,
				Start:  i,
				End:    -1,
			})
			parsed.byNumber[number] = index
			stack = append(stack, frame{number: number, start: i, capturing: true})
			i++

		case ')':
			if len(stack) == 0 {
				return nil, parsed.syntaxError(i, ErrUnbalancedGroups, "unmatched ')'")
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.capturing {
				parsed.groups[parsed.byNumber[top.number]].End = i + 1
			}
			i++

		default:
			i++
		}
	}

	if len(stack) > 0 {
		top := stack[len(stack)-1]
		detail := "unclosed group"
		if top.capturing {
			detail = fmt.Sprintf("unclosed group %d", top.number)
		}
		return nil, parsed.syntaxError(top.start, ErrUnbalancedGroups, detail)
	}
	return parsed, nil
}

// namedGroupName recognizes named capturing group prefixes and returns the
// offset of the first character in the group body.
func namedGroupName(pattern string, start int) (string, int, bool) {
	for _, prefix := range []string{"(?P<", "(?<", "(?'"} {
		if !strings.HasPrefix(pattern[start:], prefix) {
			continue
		}
		if prefix == "(?<" && start+3 < len(pattern) {
			if pattern[start+3] == '=' || pattern[start+3] == '!' {
				return "", start, false
			}
		}
		close := byte('>')
		if prefix == "(?'" {
			close = '\''
		}
		name, next, ok := readDelimited(pattern, start+len(prefix)-1, close)
		if ok && name != "" {
			return name, next, true
		}
		return "", start, false
	}
	return "", start, false
}

func (parsed *parsedPattern) scanEscape(start int, stack []frame) (int, error) {
	pattern := parsed.pattern
	if start+1 >= len(pattern) {
		return start + 1, nil
	}

	switch pattern[start+1] {
	case 'Q':
		for i := start + 2; i+1 < len(pattern); i++ {
			if pattern[i] == '\\' && pattern[i+1] == 'E' {
				return i + 2, nil
			}
		}
		return len(pattern), nil

	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		end := start + 1
		for end < len(pattern) && isDigit(pattern[end]) {
			end++
		}
		text := pattern[start+1 : end]
		number, err := strconv.Atoi(text)
		if err == nil {
			parsed.refs = append(parsed.refs, rawReference{
				start:      start,
				end:        end,
				number:     number,
				numberText: text,
				numeric:    true,
				enclosing:  innermostGroup(stack),
			})
		}
		return end, nil

	case 'k':
		if start+2 < len(pattern) {
			switch pattern[start+2] {
			case '<', '\'', '{':
				return parsed.readDelimitedReference(start, start+2, stack)
			}
		}

	case 'g':
		if start+2 < len(pattern) {
			switch pattern[start+2] {
			case '<', '{', '\'':
				return parsed.readDelimitedReference(start, start+2, stack)
			default:
				if isDigit(pattern[start+2]) {
					end := start + 2
					for end < len(pattern) && isDigit(pattern[end]) {
						end++
					}
					text := pattern[start+2 : end]
					if number, err := strconv.Atoi(text); err == nil && number > 0 {
						parsed.refs = append(parsed.refs, rawReference{
							start:      start,
							end:        end,
							number:     number,
							numberText: text,
							numeric:    true,
							enclosing:  innermostGroup(stack),
						})
					}
					return end, nil
				}
			}
		}
	}
	return start + 2, nil
}

func (parsed *parsedPattern) readDelimitedReference(start, open int, stack []frame) (int, error) {
	openByte := parsed.pattern[open]
	closeByte := byte('>')
	switch openByte {
	case '{':
		closeByte = '}'
	case '\'':
		closeByte = '\''
	}

	content, next, ok := readDelimited(parsed.pattern, open, closeByte)
	if !ok {
		return len(parsed.pattern), parsed.syntaxError(start, ErrUnbalancedGroups, "unterminated backreference")
	}

	reference := rawReference{
		start:     start,
		end:       next,
		enclosing: innermostGroup(stack),
	}
	if isAllDigits(content) {
		number, err := strconv.Atoi(content)
		if err == nil && number > 0 {
			reference.number = number
			reference.numberText = content
			reference.numeric = true
			parsed.refs = append(parsed.refs, reference)
		}
		return next, nil
	}
	if content != "" && (content[0] == '+' || content[0] == '-') {
		if delta, err := strconv.Atoi(content); err == nil {
			number := len(parsed.groups) + delta
			if number > 0 {
				reference.number = number
				reference.numberText = strconv.Itoa(number)
				reference.numeric = true
				parsed.refs = append(parsed.refs, reference)
			}
			return next, nil
		}
	}
	reference.name = content
	parsed.refs = append(parsed.refs, reference)
	return next, nil
}

func (parsed *parsedPattern) resolve(raw rawReference) (Backreference, bool, error) {
	reference := Backreference{
		Offset:         raw.start,
		Token:          parsed.pattern[raw.start:raw.end],
		EnclosingGroup: raw.enclosing,
	}

	var target *Group
	if raw.numeric {
		for length := len(raw.numberText); length >= 1; length-- {
			number, err := strconv.Atoi(raw.numberText[:length])
			if err != nil {
				continue
			}
			if index, ok := parsed.byNumber[number]; ok {
				target = &parsed.groups[index]
				break
			}
		}
		if target == nil {
			// A numeric escape that does not name a group is a literal or
			// octal escape in common regex dialects, not a backreference.
			return reference, false, nil
		}
		reference.Target = target.Number
	} else {
		indexes, ok := parsed.byName[raw.name]
		if !ok || len(indexes) == 0 {
			return reference, true, parsed.referenceError(raw, 0, raw.name, ErrUnknownGroup)
		}
		target = parsed.selectNamedTarget(indexes, raw.start)
		reference.Target = target.Number
		reference.TargetName = target.Name
	}

	reference.TargetClosed = target.End != -1 && target.End <= raw.start
	switch {
	case raw.start < target.Start:
		return reference, true, parsed.referenceError(raw, target.Number, target.Name, ErrBackreferenceToFutureGroup)
	case raw.start < target.End:
		return reference, true, parsed.referenceError(raw, target.Number, target.Name, ErrBackreferenceToOpenGroup)
	default:
		return reference, true, nil
	}
}

func (parsed *parsedPattern) selectNamedTarget(indexes []int, offset int) *Group {
	for _, index := range indexes {
		group := &parsed.groups[index]
		if group.End == -1 || (offset >= group.Start && offset < group.End) {
			return group
		}
	}
	for _, index := range indexes {
		group := &parsed.groups[index]
		if group.End != -1 && group.End <= offset {
			return group
		}
	}
	return &parsed.groups[indexes[0]]
}

func (parsed *parsedPattern) syntaxError(offset int, reason error, detail string) error {
	line, column := lineColumn(parsed.pattern, offset)
	return &ValidationError{
		Pattern: parsed.pattern,
		Offset:  offset,
		Line:    line,
		Column:  column,
		Reason:  reason,
		Detail:  detail,
	}
}

func (parsed *parsedPattern) referenceError(raw rawReference, target int, name string, reason error) error {
	line, column := lineColumn(parsed.pattern, raw.start)
	return &ValidationError{
		Pattern:        parsed.pattern,
		Offset:         raw.start,
		Line:           line,
		Column:         column,
		Target:         target,
		TargetName:     name,
		EnclosingGroup: raw.enclosing,
		Reason:         reason,
	}
}

func innermostGroup(stack []frame) int {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].capturing {
			return stack[i].number
		}
	}
	return 0
}

func readDelimited(pattern string, open int, close byte) (string, int, bool) {
	if open < 0 || open >= len(pattern) {
		return "", open, false
	}
	for i := open + 1; i < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			continue
		}
		if pattern[i] == close {
			return pattern[open+1 : i], i + 1, true
		}
	}
	return "", len(pattern), false
}

func skipComment(pattern string, start int) (int, bool) {
	for i := start; i < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			continue
		}
		if pattern[i] == ')' {
			return i + 1, true
		}
	}
	return len(pattern), false
}

func skipGroupExpression(pattern string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = skipCharacterClass(pattern, i) - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return len(pattern), false
}

func skipCharacterClass(pattern string, start int) int {
	i := start + 1
	if i < len(pattern) && pattern[i] == '^' {
		i++
	}
	first := true
	for i < len(pattern) {
		switch pattern[i] {
		case '\\':
			if i+1 < len(pattern) {
				i += 2
			} else {
				return len(pattern)
			}
			first = false

		case ']':
			if !first {
				return i + 1
			}
			first = false
			i++

		case '[':
			if i+1 < len(pattern) && (pattern[i+1] == ':' || pattern[i+1] == '.' || pattern[i+1] == '=') {
				terminator := string(pattern[i+1]) + "]"
				if end := strings.Index(pattern[i+2:], terminator); end >= 0 {
					i += 2 + end + 2
					first = false
					continue
				}
			}
			first = false
			i++

		default:
			first = false
			i++
		}
	}
	return len(pattern)
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !isDigit(value[i]) {
			return false
		}
	}
	return true
}

func lineColumn(pattern string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(pattern) {
		offset = len(pattern)
	}
	line, column := 1, 1
	for i := 0; i < offset; i++ {
		if pattern[i] == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return line, column
}
