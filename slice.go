package netsec

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// SNSSAI identifies a network slice by its slice/service type (SST) and
// optional slice differentiator (SD). SD is a three-octet value represented as
// six hexadecimal digits. An empty SD means that the S-NSSAI has no SD.
//
// SST and SD are both identity components. In particular, an S-NSSAI with SD
// "000001" is not the same as one with SD "000002", "1", or no SD at all.
type SNSSAI struct {
	SST uint8
	SD  string
}

// NetworkSlice and Slice are aliases for SNSSAI.
type NetworkSlice = SNSSAI
type Slice = SNSSAI

// Snssai and SNssai are commonly used Go spellings of S-NSSAI.
type Snssai = SNSSAI
type SNssai = SNSSAI

// NewSNSSAI constructs an S-NSSAI. Invalid SD values are retained verbatim and
// will be rejected by authorization; call Normalize to validate eagerly.
func NewSNSSAI(sst uint8, sd string) SNSSAI {
	return SNSSAI{SST: sst, SD: sd}
}

// ParseSNSSAI parses the "SST-SD" form used by many management APIs. Examples
// include "1", "1-000001", and "01-ABCDEF". A missing SD is represented by an
// empty string.
func ParseSNSSAI(value string) (SNSSAI, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return SNSSAI{}, fmt.Errorf("empty S-NSSAI")
	}

	parts := strings.SplitN(value, "-", 2)
	sst, err := strconv.ParseUint(parts[0], 10, 8)
	if err != nil {
		return SNSSAI{}, fmt.Errorf("invalid SST %q: %w", parts[0], err)
	}

	sd := ""
	if len(parts) == 2 {
		sd = parts[1]
	}
	return SNSSAI{SST: uint8(sst), SD: sd}.Normalize()
}

// Normalize validates the S-NSSAI and returns its canonical form. SD values
// are compared as octets: the hexadecimal representation is case-insensitive,
// but neither leading zero removal nor aliasing of an absent SD is performed.
func (s SNSSAI) Normalize() (SNSSAI, error) {
	sd, err := normalizeSD(s.SD)
	if err != nil {
		return SNSSAI{}, err
	}
	return SNSSAI{SST: s.SST, SD: sd}, nil
}

// Equal compares two S-NSSAIs after strict SD normalization.
func (s SNSSAI) Equal(other SNSSAI) bool {
	left, err := s.Normalize()
	if err != nil {
		return false
	}
	right, err := other.Normalize()
	if err != nil {
		return false
	}
	return left == right
}

// String returns the canonical management representation of the S-NSSAI.
func (s SNSSAI) String() string {
	if s.SD == "" {
		return strconv.Itoa(int(s.SST))
	}
	return fmt.Sprintf("%d-%s", s.SST, strings.ToUpper(strings.TrimSpace(s.SD)))
}

func normalizeSD(raw string) (string, error) {
	sd := strings.TrimSpace(raw)
	if sd == "" {
		return "", nil
	}
	if len(sd) != 6 {
		return "", fmt.Errorf("SD %q must contain exactly six hexadecimal digits", raw)
	}
	if _, err := hex.DecodeString(sd); err != nil {
		return "", fmt.Errorf("SD %q is not hexadecimal: %w", raw, err)
	}
	return strings.ToUpper(sd), nil
}
