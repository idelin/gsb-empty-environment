package displaycommit

import "math/big"

// Result aliases and common entry-point names make the package easier to
// import from small front-end adapters and generated test harnesses.
type Result = Report
type VerificationResult = Report
type CheckResult = Report

// CheckRule treats variadic messages as the complete small domain.
func CheckRule(table *TypeTable, rule *DisplayRule, messages ...*Message) *Report {
	return VerifySmallDomain(table, rule, messages)
}

// ComparePair performs the exhaustive two-message comparison used by small
// domain audits.
func ComparePair(table *TypeTable, rule *DisplayRule, left, right *Message) *Report {
	return VerifySmallDomain(table, rule, []*Message{left, right})
}

func VerifyDisplayRule(table *TypeTable, rule *DisplayRule, messages []*Message) *Report {
	return VerifySmallDomain(table, rule, messages)
}

func VerifyRules(table *TypeTable, rule *DisplayRule, messages []*Message) *Report {
	return VerifySmallDomain(table, rule, messages)
}

func CheckDisplayRule(table *TypeTable, rule *DisplayRule, messages ...*Message) *Report {
	return VerifySmallDomain(table, rule, messages)
}

func Analyze(table *TypeTable, rule *DisplayRule, messages []*Message) *Report {
	return VerifySmallDomain(table, rule, messages)
}

func AnalyzeRules(table *TypeTable, rule *DisplayRule, messages []*Message) *Report {
	return VerifySmallDomain(table, rule, messages)
}

// Verifier is a small fluent wrapper around VerifySmallDomain.
type Verifier struct {
	Table   *TypeTable
	Rule    *DisplayRule
	Options VerifyOptions
}

func NewVerifier(table *TypeTable, rule *DisplayRule) *Verifier {
	return &Verifier{Table: table, Rule: rule, Options: VerifyOptions{Exhaustive: true}}
}

func (v *Verifier) WithOptions(opt VerifyOptions) *Verifier {
	v.Options = opt
	return v
}

func (v *Verifier) Verify(messages ...*Message) *Report {
	return VerifyRule(v.Table, v.Rule, messages, v.Options)
}

func (v *Verifier) VerifySlice(messages []*Message) *Report {
	return VerifyRule(v.Table, v.Rule, messages, v.Options)
}

// NewDisplayCommitment is a terminology alias for NewSigningRequest.
func NewDisplayCommitment(table *TypeTable, rule *DisplayRule, msg *Message, nonce ...any) (*SigningRequest, error) {
	return NewSigningRequest(table, rule, msg, nonce...)
}

// BuildRequest accepts table/rule/message in either common order.
func BuildRequest(parts ...any) (*SigningRequest, error) {
	return CreateSigningRequest(parts...)
}

// AmountRange is the closed integer interval used for amount groups.
type AmountRange = Interval

func NewAmountRange(lo, hi *big.Int) Interval { return ClosedInterval(lo, hi) }

// UnknownVerdict is useful in switch statements that prefer an explicit name.
const UnknownVerdict = VerdictUnknown

// SmallDomain is an explicitly finite, exhaustive set of legal messages.
type SmallDomain struct {
	Table    *TypeTable
	Messages []*Message
}

func NewSmallDomain(table *TypeTable, messages ...*Message) *SmallDomain {
	return &SmallDomain{Table: table, Messages: messages}
}

func (d *SmallDomain) Verify(rule *DisplayRule) *Report {
	return VerifySmallDomain(d.Table, rule, d.Messages)
}

func (d *SmallDomain) VerifyWith(rule *DisplayRule, opt VerifyOptions) *Report {
	return VerifyRule(d.Table, rule, d.Messages, opt)
}

// DisplayCommitment is a terminology alias for the signed request bundle.
type DisplayCommitment = SigningRequest
