package condbuilder

import (
	"fmt"
	"strings"
)

// Operator enumerates the comparison operators supported in WHERE clauses.
// Only this fixed set is ever emitted; callers cannot supply raw SQL.
type Operator string

const (
	OpEq      Operator = "="
	OpNeq     Operator = "<>"
	OpGt      Operator = ">"
	OpGte     Operator = ">="
	OpLt      Operator = "<"
	OpLte     Operator = "<="
	OpIn      Operator = "IN"
	OpNotIn   Operator = "NOT IN"
	OpIs      Operator = "IS"
	OpIsNot   Operator = "IS NOT"
	OpLike    Operator = "LIKE"
	OpNotLike Operator = "NOT LIKE"
)

// LikeEscapeChar is the mandatory ESCAPE character for LIKE predicates.
const LikeEscapeChar = '\\'

// Filter is one WHERE predicate. Field must be a whitelisted logical name;
// Value is always bound through a placeholder and is never concatenated into
// the SQL text.
type Filter struct {
	Field string
	Op    Operator
	// Value holds the bound parameter(s). For IN/NOT IN it must be a
	// non-empty slice; for IS/IS NOT it must be nil; for LIKE predicates the
	// value is expected to already be an escapeLike result.
	Value any
}

// escapeLike escapes the LIKE meta-characters %, _ and the escape character
// itself so that user input is matched literally. The ESCAPE clause emitted
// alongside the predicate tells the database how to interpret the backslashes.
func escapeLike(input string) string {
	var b strings.Builder
	b.Grow(len(input))
	for _, r := range input {
		switch r {
		case LikeEscapeChar, '%', '_':
			b.WriteRune(LikeEscapeChar)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Contains builds a case-sensitive substring filter: field LIKE %escaped%.
func Contains(field, substring string) Filter {
	return likeFilter(field, OpLike, "%"+escapeLike(substring)+"%")
}

// StartsWith builds a prefix filter: field LIKE escaped%.
func StartsWith(field, prefix string) Filter {
	return likeFilter(field, OpLike, escapeLike(prefix)+"%")
}

// EndsWith builds a suffix filter: field LIKE %escaped.
func EndsWith(field, suffix string) Filter {
	return likeFilter(field, OpLike, "%"+escapeLike(suffix))
}

// NotContains is the negated form of Contains.
func NotContains(field, substring string) Filter {
	return likeFilter(field, OpNotLike, "%"+escapeLike(substring)+"%")
}

func likeFilter(field string, op Operator, pattern string) Filter {
	return Filter{Field: field, Op: op, Value: pattern}
}

// In builds an IN predicate from a batch of values. An empty batch is rejected
// at build time rather than silently producing "IN ()" (invalid SQL) or an
// always-true predicate.
func In(field string, values []any) Filter {
	return Filter{Field: field, Op: OpIn, Value: values}
}

// NotIn builds a NOT IN predicate; the same non-empty batch rule applies.
func NotIn(field string, values []any) Filter {
	return Filter{Field: field, Op: OpNotIn, Value: values}
}

// IsNull / IsNotNull cover the IS [NOT] NULL predicates.
func IsNull(field string) Filter    { return Filter{Field: field, Op: OpIs, Value: nil} }
func IsNotNull(field string) Filter { return Filter{Field: field, Op: OpIsNot, Value: nil} }

// Eq is shorthand for an equality predicate. Neq/Gt/Gte/Lt/Lte follow the
// same pattern via Filter literals with the matching Operator constant.
func Eq(field string, value any) Filter {
	return Filter{Field: field, Op: OpEq, Value: value}
}

// OpFilter builds a filter with an explicit operator; used for the comparison
// operators that have no dedicated helper.
func OpFilter(field string, op Operator, value any) Filter {
	return Filter{Field: field, Op: op, Value: value}
}

func (f Filter) render(w *Whitelist) (string, []any, error) {
	ident, err := w.render(f.Field)
	if err != nil {
		return "", nil, err
	}
	switch f.Op {
	case OpEq, OpNeq, OpGt, OpGte, OpLt, OpLte:
		if f.Value == nil {
			return "", nil, fmt.Errorf("field %q: operator %s needs a non-nil value", f.Field, f.Op)
		}
		return ident + " " + string(f.Op) + " ?", []any{f.Value}, nil

	case OpIs, OpIsNot:
		if f.Value != nil {
			return "", nil, fmt.Errorf("field %q: operator %s requires nil value", f.Field, f.Op)
		}
		return ident + " " + string(f.Op) + " NULL", nil, nil

	case OpLike, OpNotLike:
		pattern, ok := f.Value.(string)
		if !ok {
			return "", nil, fmt.Errorf("field %q: LIKE value must be a string", f.Field)
		}
		sql := ident + " " + string(f.Op) + " ? ESCAPE " + "'" + string(LikeEscapeChar) + "'"
		return sql, []any{pattern}, nil

	case OpIn, OpNotIn:
		vals, err := normalizeBatch(f.Field, f.Value)
		if err != nil {
			return "", nil, err
		}
		placeholders := strings.Repeat("?, ", len(vals))
		placeholders = strings.TrimSuffix(placeholders, ", ")
		return ident + " " + string(f.Op) + " (" + placeholders + ")", vals, nil

	default:
		return "", nil, fmt.Errorf("field %q: unsupported operator %q", f.Field, f.Op)
	}
}

func normalizeBatch(field string, v any) ([]any, error) {
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("field %q: IN/NOT IN requires []any batch", field)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("field %q: IN/NOT IN batch must not be empty", field)
	}
	for i, item := range raw {
		if item == nil {
			return nil, fmt.Errorf("field %q: IN/NOT IN batch element %d is nil (use IS NULL)", field, i)
		}
	}
	return raw, nil
}
