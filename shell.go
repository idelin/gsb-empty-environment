package netsec

import "strings"

// ShellQuote returns value quoted for use as one POSIX shell word. It uses
// single quotes and replaces each single quote with the four-character
// sequence closing-quote, backslash-escaped quote, reopening-quote:
//
//	'  ->  '\''
//
// ShellQuote is only for callers that explicitly wrap a hook in a shell. The
// InvokeHook path does not use it or otherwise involve a shell.
func ShellQuote(value string) string {
	return "'" + EscapeSingleQuotes(value) + "'"
}

// EscapeSingleQuotes escapes single quotes for a value that is already inside
// a pair of single quotes. It does not add the surrounding quotes; most
// callers should use ShellQuote.
func EscapeSingleQuotes(value string) string {
	return strings.ReplaceAll(value, "'", `'\''`)
}
