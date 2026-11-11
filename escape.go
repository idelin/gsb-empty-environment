package ldapfilter

import "strings"

// hexdigits holds the lowercase hexadecimal digits used by RFC 4515 escapes.
var hexdigits = "0123456789abcdef"

// EscapeValue escapes an assertion value for embedding in an LDAP search
// filter according to RFC 4515 section 3.
//
// The filter-specific characters '*', '(' and ')', the NUL byte and the
// backslash are always escaped as a backslash followed by two hexadecimal
// digits. Every other byte, including arbitrary UTF-8 continuation bytes, is
// left untouched, so user input can never alter filter structure.
func EscapeValue(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for index := 0; index < len(value); index++ {
		ch := value[index]
		switch ch {
		case '*', '(', ')', '\\', 0:
			builder.WriteByte('\\')
			builder.WriteByte(hexdigits[ch>>4])
			builder.WriteByte(hexdigits[ch&0x0f])
		default:
			builder.WriteByte(ch)
		}
	}
	return builder.String()
}
