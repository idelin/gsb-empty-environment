package noise

import "fmt"

// Pattern identifies a supported Noise handshake pattern.
type Pattern string

const (
	PatternXX         Pattern = "XX"
	PatternKK         Pattern = "KK"
	PatternIK         Pattern = "IK"
	PatternXXfallback Pattern = "XXfallback"
)

const cipherSuiteName = "25519_ChaChaPoly_SHA256"

// ProtocolName returns the full Noise protocol name for the pattern and the
// fixed 25519 + ChaChaPoly + SHA256 cipher suite.
func (p Pattern) ProtocolName() string {
	return "Noise_" + string(p) + "_" + cipherSuiteName
}

type token uint8

const (
	tokenE token = iota
	tokenS
	tokenEE
	tokenES
	tokenSE
	tokenSS
)

func (t token) String() string {
	switch t {
	case tokenE:
		return "e"
	case tokenS:
		return "s"
	case tokenEE:
		return "ee"
	case tokenES:
		return "es"
	case tokenSE:
		return "se"
	case tokenSS:
		return "ss"
	default:
		return "?"
	}
}

type preToken struct {
	initiator bool
	token     token
}

type patternSpec struct {
	pattern    Pattern
	pre        []preToken
	messages   [][]token
	usesFallbk bool
}

func lookupPattern(p Pattern) (*patternSpec, error) {
	switch p {
	case PatternXX:
		return &patternSpec{
			pattern:  PatternXX,
			messages: [][]token{{tokenE}, {tokenE, tokenEE, tokenS, tokenES}, {tokenS, tokenSE}},
		}, nil
	case PatternKK:
		return &patternSpec{
			pattern: PatternKK,
			pre: []preToken{
				{initiator: true, token: tokenS},
				{initiator: false, token: tokenS},
			},
			messages: [][]token{{tokenE, tokenES, tokenSS}, {tokenE, tokenEE, tokenSE}},
		}, nil
	case PatternIK:
		return &patternSpec{
			pattern: PatternIK,
			pre: []preToken{
				{initiator: false, token: tokenS},
			},
			messages: [][]token{{tokenE, tokenES, tokenS, tokenSS}, {tokenE, tokenEE, tokenSE}},
		}, nil
	case PatternXXfallback:
		return &patternSpec{
			pattern: PatternXXfallback,
			pre: []preToken{
				{initiator: false, token: tokenS},
			},
			messages: [][]token{
				{tokenE},
				{tokenE, tokenEE, tokenS, tokenES},
				{tokenS, tokenSE},
			},
			usesFallbk: true,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedPattern, p)
	}
}
