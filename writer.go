package ldapfilter

import (
	"fmt"
	"strings"
)

func writeFilter(builder *strings.Builder, filter Filter) error {
	switch node := filter.(type) {
	case nil:
		return fmt.Errorf("ldapfilter: nil filter")
	case *AndFilter:
		return writeSet(builder, '&', node.Children)
	case *OrFilter:
		return writeSet(builder, '|', node.Children)
	case *NotFilter:
		if node.Child == nil {
			return fmt.Errorf("ldapfilter: not filter requires a child")
		}
		builder.WriteString("(!")
		if err := writeFilter(builder, node.Child); err != nil {
			return err
		}
		builder.WriteByte(')')
	case *EqualityFilter:
		if err := validateAttribute(node.Attribute); err != nil {
			return err
		}
		builder.WriteByte('(')
		builder.WriteString(node.Attribute)
		builder.WriteByte('=')
		builder.WriteString(EscapeValue(node.Value))
		builder.WriteByte(')')
	case *SubstringsFilter:
		if err := validateAttribute(node.Attribute); err != nil {
			return err
		}
		builder.WriteByte('(')
		builder.WriteString(node.Attribute)
		builder.WriteByte('=')
		builder.WriteString(EscapeValue(node.Initial))
		for _, piece := range node.Any {
			builder.WriteByte('*')
			builder.WriteString(EscapeValue(piece))
		}
		builder.WriteByte('*')
		builder.WriteString(EscapeValue(node.Final))
		builder.WriteByte(')')
	case *PresentFilter:
		if err := validateAttribute(node.Attribute); err != nil {
			return err
		}
		builder.WriteByte('(')
		builder.WriteString(node.Attribute)
		builder.WriteString("=*)")
	case *ApproxFilter:
		if err := validateAttribute(node.Attribute); err != nil {
			return err
		}
		builder.WriteByte('(')
		builder.WriteString(node.Attribute)
		builder.WriteString("~=")
		builder.WriteString(EscapeValue(node.Value))
		builder.WriteByte(')')
	case *GreaterOrEqualFilter:
		if err := validateAttribute(node.Attribute); err != nil {
			return err
		}
		builder.WriteByte('(')
		builder.WriteString(node.Attribute)
		builder.WriteString(">=")
		builder.WriteString(EscapeValue(node.Value))
		builder.WriteByte(')')
	case *LessOrEqualFilter:
		if err := validateAttribute(node.Attribute); err != nil {
			return err
		}
		builder.WriteByte('(')
		builder.WriteString(node.Attribute)
		builder.WriteString("<=")
		builder.WriteString(EscapeValue(node.Value))
		builder.WriteByte(')')
	case *ExtensibleFilter:
		return writeExtensible(builder, node)
	default:
		return fmt.Errorf("ldapfilter: unsupported filter type %T", filter)
	}
	return nil
}

func writeSet(builder *strings.Builder, operator byte, children []Filter) error {
	if len(children) == 0 {
		return fmt.Errorf("ldapfilter: %q filter requires at least one child", operator)
	}
	builder.WriteByte('(')
	builder.WriteByte(operator)
	for _, child := range children {
		if child == nil {
			return fmt.Errorf("ldapfilter: nil child in filter set")
		}
		if err := writeFilter(builder, child); err != nil {
			return err
		}
	}
	builder.WriteByte(')')
	return nil
}

func writeExtensible(builder *strings.Builder, node *ExtensibleFilter) error {
	if node.Attribute == "" && node.MatchingRule == "" {
		return fmt.Errorf("ldapfilter: extensible match requires an attribute or a matching rule")
	}
	if node.Attribute != "" {
		if err := validateAttribute(node.Attribute); err != nil {
			return err
		}
	}
	if node.MatchingRule != "" {
		if err := validateOID(node.MatchingRule); err != nil {
			return err
		}
	}
	builder.WriteByte('(')
	if node.Attribute != "" {
		builder.WriteString(node.Attribute)
	}
	if node.DNAttributes {
		builder.WriteString(":dn")
	}
	if node.MatchingRule != "" {
		builder.WriteByte(':')
		builder.WriteString(node.MatchingRule)
	}
	builder.WriteByte(':')
	builder.WriteByte('=')
	builder.WriteString(EscapeValue(node.Value))
	builder.WriteByte(')')
	return nil
}

// validateAttribute checks an AttributeDescription: a keystring or dotted OID
// optionally followed by ";keystring" options.
func validateAttribute(attribute string) error {
	if attribute == "" {
		return fmt.Errorf("ldapfilter: empty attribute description")
	}
	segments := strings.Split(attribute, ";")
	for index, segment := range segments {
		if segment == "" {
			return fmt.Errorf("ldapfilter: invalid attribute description %q", attribute)
		}
		var err error
		if index == 0 {
			err = validateNameOrOID(segment)
		} else {
			err = validateKeyString(segment)
		}
		if err != nil {
			return fmt.Errorf("ldapfilter: invalid attribute description %q", attribute)
		}
	}
	return nil
}

func validateNameOrOID(token string) error {
	if isNumericOID(token) {
		return nil
	}
	return validateKeyString(token)
}

func validateOID(oid string) error {
	if isNumericOID(oid) {
		return nil
	}
	return validateKeyString(oid)
}

func isNumericOID(token string) bool {
	numbers := strings.Split(token, ".")
	if len(numbers) < 2 {
		return false
	}
	for _, number := range numbers {
		if number == "" {
			return false
		}
		for _, ch := range number {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func validateKeyString(token string) error {
	if token == "" {
		return fmt.Errorf("ldapfilter: empty key string")
	}
	for index, ch := range token {
		if isAlpha(ch) || (index > 0 && isDigit(ch)) || ch == '-' {
			continue
		}
		return fmt.Errorf("ldapfilter: invalid key string %q", token)
	}
	return nil
}

func isAlpha(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}
