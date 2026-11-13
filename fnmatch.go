package netsec

import (
	"strings"
	"unicode"
)

type patternTokenKind uint8

const (
	patternLiteral patternTokenKind = iota
	patternStar
	patternAnyOne
	patternClass
)

type runeRange struct {
	low  rune
	high rune
}

type patternToken struct {
	kind    patternTokenKind
	literal rune
	ranges  []runeRange
	negated bool
}

// fnmatch reports whether value matches pattern. Unlike filepath.Match, the
// wildcard '*' matches every character, including '/' and spaces.
func fnmatch(pattern, value string) bool {
	tokens := compilePattern(pattern)
	valueRunes := []rune(value)

	previous := make([]bool, len(valueRunes)+1)
	previous[0] = true
	for _, token := range tokens {
		current := make([]bool, len(valueRunes)+1)
		switch token.kind {
		case patternStar:
			current[0] = previous[0]
			for index := 1; index <= len(valueRunes); index++ {
				current[index] = previous[index] || current[index-1]
			}
		case patternAnyOne:
			for index := 1; index <= len(valueRunes); index++ {
				current[index] = previous[index-1]
			}
		case patternLiteral:
			for index := 1; index <= len(valueRunes); index++ {
				current[index] = previous[index-1] && token.literal == valueRunes[index-1]
			}
		case patternClass:
			for index := 1; index <= len(valueRunes); index++ {
				if !previous[index-1] {
					continue
				}
				matched := token.rangesMatch(valueRunes[index-1])
				if token.negated {
					matched = !matched
				}
				current[index] = matched
			}
		}
		previous = current
	}
	return previous[len(valueRunes)]
}

func compilePattern(pattern string) []patternToken {
	runes := []rune(pattern)
	tokens := make([]patternToken, 0, len(runes))

	for index := 0; index < len(runes); index++ {
		switch runes[index] {
		case '\\':
			if index+1 < len(runes) {
				index++
				tokens = append(tokens, patternToken{kind: patternLiteral, literal: runes[index]})
			} else {
				tokens = append(tokens, patternToken{kind: patternLiteral, literal: '\\'})
			}
		case '*':
			if len(tokens) == 0 || tokens[len(tokens)-1].kind != patternStar {
				tokens = append(tokens, patternToken{kind: patternStar})
			}
		case '?':
			tokens = append(tokens, patternToken{kind: patternAnyOne})
		case '[':
			end := findClassEnd(runes, index+1)
			if end < 0 {
				tokens = append(tokens, patternToken{kind: patternLiteral, literal: '['})
				continue
			}
			token, ok := parseClass(runes[index+1 : end])
			if !ok {
				tokens = append(tokens, patternToken{kind: patternLiteral, literal: '['})
				continue
			}
			tokens = append(tokens, token)
			index = end
		default:
			tokens = append(tokens, patternToken{kind: patternLiteral, literal: runes[index]})
		}
	}
	return tokens
}

func findClassEnd(runes []rune, start int) int {
	if start < len(runes) && (runes[start] == '!' || runes[start] == '^') {
		start++
	}
	if start < len(runes) && runes[start] == ']' {
		start++
	}
	for index := start; index < len(runes); index++ {
		if runes[index] == '\\' {
			index++
			continue
		}
		if runes[index] == ']' {
			return index
		}
	}
	return -1
}

func parseClass(runes []rune) (patternToken, bool) {
	token := patternToken{kind: patternClass}
	index := 0
	if len(runes) > 0 && (runes[0] == '!' || runes[0] == '^') {
		token.negated = true
		index++
	}

	for index < len(runes) {
		first := runes[index]
		if first == '\\' && index+1 < len(runes) {
			index++
			first = runes[index]
		}
		index++

		low, high := first, first
		if index+1 < len(runes) && runes[index] == '-' && runes[index+1] != ']' {
			high = runes[index+1]
			if high == '\\' && index+2 < len(runes) {
				high = runes[index+2]
				index++
			}
			index += 2
		}
		if low > high {
			low, high = high, low
		}
		token.ranges = append(token.ranges, runeRange{low: low, high: high})
	}

	return token, len(token.ranges) > 0
}

func (token patternToken) rangesMatch(value rune) bool {
	for _, item := range token.ranges {
		if value >= item.low && value <= item.high {
			return true
		}
	}
	return false
}

func patternHasWildcard(pattern string) bool {
	for _, token := range compilePattern(pattern) {
		if token.kind != patternLiteral {
			return true
		}
	}
	return false
}

// patternContainsLiteralOption reports whether option is present as an
// uninterrupted literal run in pattern. Wildcard and character-class tokens do
// not count as a literal spelling of an option.
func patternContainsLiteralOption(pattern, option string) bool {
	tokens := compilePattern(pattern)
	wanted := []rune(option)
	if len(wanted) == 0 {
		return false
	}

	for start := 0; start+len(wanted) <= len(tokens); start++ {
		for offset, wantedRune := range wanted {
			token := tokens[start+offset]
			if token.kind != patternLiteral || token.literal != wantedRune {
				break
			}
			if offset == len(wanted)-1 {
				if start == 0 {
					return true
				}
				previous := tokens[start-1]
				if previous.kind != patternLiteral || unicode.IsSpace(previous.literal) {
					return true
				}
				break
			}
		}
	}
	return false
}

func hasParentDirectorySegment(argument string) bool {
	for _, segment := range strings.Split(argument, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}
