package nvmf

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateHostNQNValid(t *testing.T) {
	valid := []string{
		"nqn.2019-04.com.example:sn:abc123",
		"nqn.2014-08.org.nvmexpress:uuid:123e4567-e89b-12d3-a456-426614174000",
		// UUID hex letters are allowed in upper case as well.
		"nqn.2014-08.org.nvmexpress:uuid:123E4567-E89B-12D3-A456-426614174000",
		"nqn.2020-01.io.subsys:host-42",
		"nqn.2020-12.io.subsys:host_42.5",
		"nqn.2020-01.example:single-label-domain",
		"nqn.2020-01.com.ex-ample2:label-with-digits",
		"nqn.2020-01.123abc:digit-leading-label",
		// Additional colons belong to the user-assigned suffix.
		"nqn.2020-01.com.example:a:b:c",
		// A uuid-like suffix on a non-2014 NQN is still a generic NQN.
		"nqn.2015-09.org.nvmexpress:uuid:whatever",
		// Non-ASCII text is fine in the suffix as long as it is valid UTF-8.
		"nqn.2021-03.net.example:后缀-主机",
		// Exactly 223 bytes.
		"nqn.2020-01.com.example:" + strings.Repeat("a", 199),
	}
	for _, nqn := range valid {
		t.Run(nqn, func(t *testing.T) {
			if err := ValidateHostNQN(nqn); err != nil {
				t.Fatalf("ValidateHostNQN(%q) unexpected error: %v", nqn, err)
			}
		})
	}
}

func TestValidateHostNQNInvalid(t *testing.T) {
	tooLong := "nqn.2020-01.com.example:" + strings.Repeat("a", 200) // 224 bytes
	longLabel := "nqn.2020-01.com." + strings.Repeat("a", 64) + ":x"
	cases := []struct {
		nqn string
		err error
	}{
		{"", ErrEmptyNQN},
		{"iqn.2020-01.com.example:iscsi-style", ErrInvalidNQN},
		{"nqn.com.example:missing-date", ErrInvalidNQN},
		{"nqn.2020-13.com.example:bad-month", ErrInvalidNQN},
		{"nqn.2020-00.com.example:bad-month", ErrInvalidNQN},
		{"nqn.2020-1.com.example:short-month", ErrInvalidNQN},
		{"nqn.20ab-01.com.example:bad-year", ErrInvalidNQN},
		{"nqn.2020-01.com.example", ErrInvalidNQN}, // no colon, no suffix
		{"nqn.2020-01.com.example:", ErrInvalidNQN},
		{"nqn.2020-01..com.example:x", ErrInvalidNQN},
		{"nqn.2020-01.com.example.:x", ErrInvalidNQN},
		{"nqn.2020-01.COM.Example:x", ErrInvalidNQN}, // uppercase reverse domain
		{"nqn.2020-01.com.ex_ample:x", ErrInvalidNQN},
		{"nqn.2020-01.com.example:a b", ErrInvalidNQN},
		{"nqn.2020-01.com.example:tab\there", ErrInvalidNQN},
		{"nqn.2020-01.com.example:newline\nhere", ErrInvalidNQN},
		{" nqn.2020-01.com.example:x", ErrInvalidNQN},
		{"nqn.2020-01.com.example:x ", ErrInvalidNQN},
		{longLabel, ErrInvalidNQN},
		// Well-known discovery NQN has no user suffix and is not a host NQN.
		{"nqn.2014-08.org.nvmexpress.discovery", ErrInvalidNQN},
		// UUID-form NQNs require a canonical 36-char UUID.
		{"nqn.2014-08.org.nvmexpress:uuid:", ErrMalformedUUID},
		{"nqn.2014-08.org.nvmexpress:uuid:123e4567e89b12d3a456426614174000", ErrMalformedUUID},
		{"nqn.2014-08.org.nvmexpress:uuid:123e4567-e89b-12d3-a456-42661417400", ErrMalformedUUID},
		{"nqn.2014-08.org.nvmexpress:uuid:123e4567-e89b-12d3-g456-426614174000", ErrMalformedUUID},
		// Upper-case UUID prefix is not the well-known uuid form; it falls back
		// to the generic form, whose suffix permits upper-case letters. It is a
		// syntactically valid NQN but can never byte-match a lower-case
		// allowlist entry (covered in admission tests).
		{"nqn.2020-01.com.example:ABC", nil},
		{"nqn.2014-08.org.nvmexpress:uuid:123e4567-e89b-12d3-a456-426614174000-extra", ErrMalformedUUID},
		{tooLong, ErrNQNTooLong},
		{"nqn.2020-01.com.example:\xff\xfe", ErrNQNNotUTF8},
	}
	for _, tc := range cases {
		t.Run(tc.nqn, func(t *testing.T) {
			err := ValidateHostNQN(tc.nqn)
			if !errors.Is(err, tc.err) {
				t.Fatalf("ValidateHostNQN(%q) = %v, want %v", tc.nqn, err, tc.err)
			}
		})
	}
}

func TestValidateHostNQNLengthBoundary(t *testing.T) {
	base := "nqn.2020-01.com.example:"
	if err := ValidateHostNQN(base + strings.Repeat("a", MaxNQNLength-len(base))); err != nil {
		t.Fatalf("223-byte NQN rejected: %v", err)
	}
	if err := ValidateHostNQN(base + strings.Repeat("a", MaxNQNLength-len(base)+1)); !errors.Is(err, ErrNQNTooLong) {
		t.Fatalf("224-byte NQN: got %v, want ErrNQNTooLong", err)
	}
}
