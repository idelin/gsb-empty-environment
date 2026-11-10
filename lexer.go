package main

import "fmt"

// lexState is the state of a single-quote string lexer.
type lexState uint8

const (
	stOutside lexState = iota // O: not inside a string literal
	stInside                  // I: inside a string, ordinary byte
	stQuote                   // Q: inside, last byte was a single quote (pending close / doubled quote)
	stBack                    // B: inside, last byte was a backslash (backslash mode only)
)

func (s lexState) String() string {
	switch s {
	case stOutside:
		return "O"
	case stInside:
		return "I"
	case stQuote:
		return "Q"
	case stBack:
		return "B"
	default:
		return fmt.Sprintf("?%d", uint8(s))
	}
}

// escapeMode selects how backslashes are interpreted inside a string.
type escapeMode uint8

const (
	escOff escapeMode = iota // standard SQL: backslash is an ordinary byte, only '' quotes a quote
	escOn                    // MySQL-style: backslash escapes the next byte ('' still works)
)

// nextState is the deterministic transition table. No lookahead is required:
// a doubled quote is resolved in the stQuote state one byte later.
func nextState(mode escapeMode, s lexState, b byte) lexState {
	if mode == escOff {
		switch s {
		case stOutside:
			if b == '\'' {
				return stInside
			}
			return stOutside
		case stInside:
			if b == '\'' {
				return stQuote
			}
			return stInside
		case stQuote:
			if b == '\'' {
				return stInside // doubled quote: literal quote, still inside
			}
			return stOutside // previous quote closed the string
		default:
			return stOutside
		}
	}

	switch s {
	case stOutside:
		if b == '\'' {
			return stInside
		}
		return stOutside
	case stInside:
		switch b {
		case '\'':
			return stQuote
		case '\\':
			return stBack
		default:
			return stInside
		}
	case stQuote:
		if b == '\'' {
			return stInside // doubled quote is valid with backslash mode on as well
		}
		return stOutside // previous quote closed the string
	case stBack:
		return stInside // the escaped byte (even a quote or backslash) is string content
	default:
		return stOutside
	}
}

// isInside reports whether a byte consumed from state pre counts as string
// content. A quote read from stOutside is an opening delimiter (not content);
// a quote read from stQuote completes a doubled quote (content); any other
// byte read from stQuote lands outside the already-closed string.
func isInside(pre lexState, b byte) bool {
	switch pre {
	case stInside, stBack:
		return true
	case stQuote:
		return b == '\''
	default:
		return false
	}
}

// byteNote explains a transition for the per-byte trace.
func byteNote(mode escapeMode, pre, post lexState, b byte) string {
	switch {
	case b == ';':
		return "semicolon"
	case pre == stOutside && b == '\'':
		return "opening quote"
	case pre == stQuote && b == '\'':
		return "doubled quote"
	case pre == stQuote:
		return "string already closed"
	case pre == stBack:
		return "escaped byte"
	case mode == escOn && pre == stInside && b == '\\':
		return "backslash escape"
	case pre == stInside && b == '\'':
		return "quote"
	default:
		return ""
	}
}
