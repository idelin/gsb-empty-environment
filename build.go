package ldapfilter

import (
	"fmt"
	"strings"
)

// NewAnd builds an AND set. The set must contain at least one child.
func NewAnd(children ...Filter) (*AndFilter, error) {
	if len(children) == 0 {
		return nil, fmt.Errorf("ldapfilter: and filter requires at least one child")
	}
	return &AndFilter{Children: children}, nil
}

// NewOr builds an OR set. The set must contain at least one child.
func NewOr(children ...Filter) (*OrFilter, error) {
	if len(children) == 0 {
		return nil, fmt.Errorf("ldapfilter: or filter requires at least one child")
	}
	return &OrFilter{Children: children}, nil
}

// NewNot builds a negated filter.
func NewNot(child Filter) (*NotFilter, error) {
	if child == nil {
		return nil, fmt.Errorf("ldapfilter: not filter requires a child")
	}
	return &NotFilter{Child: child}, nil
}

// NewEquality builds an equality match assertion.
func NewEquality(attribute, value string) (*EqualityFilter, error) {
	if err := validateAttribute(attribute); err != nil {
		return nil, err
	}
	return &EqualityFilter{Attribute: attribute, Value: value}, nil
}

// NewSubstrings builds a substring assertion. The any slice holds the literal
// pieces between wildcards; empty strings encode consecutive wildcards.
func NewSubstrings(attribute, initial string, any []string, final string) (*SubstringsFilter, error) {
	if err := validateAttribute(attribute); err != nil {
		return nil, err
	}
	return &SubstringsFilter{Attribute: attribute, Initial: initial, Any: any, Final: final}, nil
}

// NewPresent builds a presence assertion.
func NewPresent(attribute string) (*PresentFilter, error) {
	if err := validateAttribute(attribute); err != nil {
		return nil, err
	}
	return &PresentFilter{Attribute: attribute}, nil
}

// NewApprox builds an approximate match assertion.
func NewApprox(attribute, value string) (*ApproxFilter, error) {
	if err := validateAttribute(attribute); err != nil {
		return nil, err
	}
	return &ApproxFilter{Attribute: attribute, Value: value}, nil
}

// NewGreaterOrEqual builds a greaterOrEqual assertion.
func NewGreaterOrEqual(attribute, value string) (*GreaterOrEqualFilter, error) {
	if err := validateAttribute(attribute); err != nil {
		return nil, err
	}
	return &GreaterOrEqualFilter{Attribute: attribute, Value: value}, nil
}

// NewLessOrEqual builds a lessOrEqual assertion.
func NewLessOrEqual(attribute, value string) (*LessOrEqualFilter, error) {
	if err := validateAttribute(attribute); err != nil {
		return nil, err
	}
	return &LessOrEqualFilter{Attribute: attribute, Value: value}, nil
}

// NewExtensible builds an extensible matching rule assertion. At least one of
// attribute or matchingRule must be supplied.
func NewExtensible(attribute, matchingRule string, dnAttributes bool, value string) (*ExtensibleFilter, error) {
	if attribute == "" && matchingRule == "" {
		return nil, fmt.Errorf("ldapfilter: extensible match requires an attribute or a matching rule")
	}
	if attribute != "" {
		if err := validateAttribute(attribute); err != nil {
			return nil, err
		}
	}
	if matchingRule != "" {
		if err := validateOID(matchingRule); err != nil {
			return nil, err
		}
	}
	return &ExtensibleFilter{
		Attribute:    attribute,
		MatchingRule: matchingRule,
		DNAttributes: dnAttributes,
		Value:        value,
	}, nil
}

// Build serializes a filter tree into its RFC 4511 string representation.
// It returns an error for malformed trees so hand-built nodes can be validated
// before the rendered string is sent to a directory.
func Build(filter Filter) (string, error) {
	var builder strings.Builder
	if err := writeFilter(&builder, filter); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// mustBuild renders a node or panics if the node is malformed. Constructors
// and Build validate inputs, so String only panics on hand-built bad nodes.
func mustBuild(filter Filter) string {
	rendered, err := Build(filter)
	if err != nil {
		panic(err)
	}
	return rendered
}

func (f *AndFilter) String() string            { return mustBuild(f) }
func (f *OrFilter) String() string             { return mustBuild(f) }
func (f *NotFilter) String() string            { return mustBuild(f) }
func (f *EqualityFilter) String() string       { return mustBuild(f) }
func (f *SubstringsFilter) String() string     { return mustBuild(f) }
func (f *PresentFilter) String() string        { return mustBuild(f) }
func (f *ApproxFilter) String() string         { return mustBuild(f) }
func (f *GreaterOrEqualFilter) String() string { return mustBuild(f) }
func (f *LessOrEqualFilter) String() string    { return mustBuild(f) }
func (f *ExtensibleFilter) String() string     { return mustBuild(f) }
