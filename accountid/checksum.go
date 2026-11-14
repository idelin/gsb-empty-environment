package accountid

import "fmt"

// checkDigits computes ISO 13616 / mod-97-10 check digits over body
// (uppercase letters and digits only).
func checkDigits(body string) string {
	return fmt.Sprintf("%02d", 98-mod97(body+"00"))
}

// verifyChecksum reports whether body+check satisfies mod 97 == 1.
func verifyChecksum(body, check string) bool {
	return mod97(body+check) == 1
}

// mod97 expands letters to digits (A=10 .. Z=35) and returns the
// remainder modulo 97, processing one decimal digit at a time so no
// big-integer arithmetic is needed. It returns -1 on invalid input.
func mod97(s string) int {
	m := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			m = (m*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			v := int(c-'A') + 10
			m = (m*10 + v/10) % 97
			m = (m*10 + v%10) % 97
		default:
			return -1
		}
	}
	return m
}
