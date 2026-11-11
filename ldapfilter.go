package ldapfilter

// Filter is an RFC 4511 LDAP search filter syntax tree node.
//
// Implementations of Filter are intentionally unexported: filters are created
// through the constructors in this package and inspected through type
// assertions on the concrete node types.
type Filter interface {
	// String renders the node as a canonical RFC 4511 filter string.
	String() string
	ast()
}

// AndFilter matches when every child filter matches (the "&" choice).
type AndFilter struct {
	Children []Filter
}

// OrFilter matches when any child filter matches (the "|" choice).
type OrFilter struct {
	Children []Filter
}

// NotFilter matches when its child filter does not (the "!" choice).
type NotFilter struct {
	Child Filter
}

// EqualityFilter is an attributeValueAssertion using "=".
type EqualityFilter struct {
	Attribute string
	Value     string
}

// SubstringsFilter is a substring filter: attr=initial*any*any*final.
// Each piece is a literal value; none may contain an unescaped '*'.
type SubstringsFilter struct {
	Attribute string
	Initial   string
	Any       []string
	Final     string
}

// PresentFilter matches entries that carry the attribute (attr=*).
type PresentFilter struct {
	Attribute string
}

// ApproxFilter is an approximate match assertion using "~=".
type ApproxFilter struct {
	Attribute string
	Value     string
}

// GreaterOrEqualFilter is a greaterOrEqual assertion using ">=".
type GreaterOrEqualFilter struct {
	Attribute string
	Value     string
}

// LessOrEqualFilter is a lessOrEqual assertion using "<=".
type LessOrEqualFilter struct {
	Attribute string
	Value     string
}

// ExtensibleFilter is an extensible matching rule assertion.
//
// At least one of Attribute or MatchingRule must be set. DNAttributes maps to
// the ":dn" option.
type ExtensibleFilter struct {
	Attribute    string
	MatchingRule string
	DNAttributes bool
	Value        string
}

func (*AndFilter) ast()            {}
func (*OrFilter) ast()             {}
func (*NotFilter) ast()            {}
func (*EqualityFilter) ast()       {}
func (*SubstringsFilter) ast()     {}
func (*PresentFilter) ast()        {}
func (*ApproxFilter) ast()         {}
func (*GreaterOrEqualFilter) ast() {}
func (*LessOrEqualFilter) ast()    {}
func (*ExtensibleFilter) ast()     {}
