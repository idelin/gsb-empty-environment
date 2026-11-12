package netsec

import "errors"

// Rule identifies the validation rule that rejected an SQE or chain.
type Rule string

const (
	RuleSQESize            Rule = "sqe_size"
	RuleFlagAllowed        Rule = "flag_not_allowed"
	RuleIndexEncoding      Rule = "fixed_index_encoding"
	RuleOpcodeWhitelist    Rule = "opcode_not_whitelisted"
	RuleFixedFileRequired  Rule = "fixed_file_required"
	RuleFixedFileForbidden Rule = "fixed_file_forbidden"
	RuleSlotRange          Rule = "slot_index_out_of_range"
	RuleSlotState          Rule = "slot_not_active"
	RuleSlotAccess         Rule = "slot_access_denied"
	RulePersonality        Rule = "personality_mismatch"
	RuleChainLink          Rule = "dangling_link"
	RuleChainCred          Rule = "chain_credential_mismatch"
	RuleChainPersonality   Rule = "chain_personality_mismatch"
	RuleEmptyBatch         Rule = "empty_batch"
)

// Sentinel errors for errors.Is matching.
var (
	ErrSQESize            = errors.New("sqe is not 64 bytes")
	ErrFlagNotAllowed     = errors.New("unsupported iosqe flag")
	ErrIndexEncoding      = errors.New("invalid fixed-file index encoding")
	ErrOpcodeNotAllowed   = errors.New("opcode not in whitelist")
	ErrFixedFileRequired  = errors.New("opcode must use IOSQE_FIXED_FILE")
	ErrFixedFileForbidden = errors.New("opcode must not use IOSQE_FIXED_FILE")
	ErrSlotOutOfRange     = errors.New("fixed-file slot index out of range")
	ErrSlotNotActive      = errors.New("slot is empty or revoked")
	ErrSlotAccessDenied   = errors.New("slot does not grant the required access")
	ErrPersonality        = errors.New("sqe personality does not match slot credential")
	ErrDanglingLink       = errors.New("linked sqe has no successor in the batch")
	ErrChainCredMismatch  = errors.New("linked chain mixes slots from different credentials")
	ErrChainPersMismatch  = errors.New("linked chain mixes personalities")
	ErrEmptyBatch         = errors.New("empty submission batch")
)

// RuleError attaches the failed rule, the batch position and detail text to
// the underlying sentinel error.
type RuleError struct {
	Rule   Rule
	Index  int // position within the validated batch, -1 when position-less
	Detail string
	cause  error
}

func (e *RuleError) Error() string {
	msg := string(e.Rule)
	if e.Index >= 0 {
		msg += " at sqe[" + itoa(e.Index) + "]"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

func (e *RuleError) Unwrap() error { return e.cause }

func ruleErr(index int, rule Rule, cause error, detail string) *RuleError {
	return &RuleError{Rule: rule, Index: index, Detail: detail, cause: cause}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
