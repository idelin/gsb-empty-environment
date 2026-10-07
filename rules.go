package validag

import "fmt"

// Required fails when the field is absent from the input.
func Required(field, id string) Rule {
	return Rule{
		ID:    id,
		Field: field,
		Check: func(v Value) error {
			if !v.Present {
				return fmt.Errorf("field is required")
			}
			return nil
		},
	}
}

// IsString fails when the field value is not a string.
func IsString(field, id string, deps ...string) Rule {
	return Rule{
		ID:              id,
		Field:           field,
		Depends:         deps,
		SkipWhenMissing: true,
		Check: func(v Value) error {
			if _, ok := v.Raw.(string); !ok {
				return fmt.Errorf("expected string, got %T", v.Raw)
			}
			return nil
		},
	}
}

// MinLen fails when a string field is shorter than n.
func MinLen(field, id string, n int, deps ...string) Rule {
	return Rule{
		ID:              id,
		Field:           field,
		Depends:         deps,
		SkipWhenMissing: true,
		Check: func(v Value) error {
			s, _ := v.Raw.(string)
			if len(s) < n {
				return fmt.Errorf("length %d is below minimum %d", len(s), n)
			}
			return nil
		},
	}
}

// IsInt fails when the field value is not an int.
func IsInt(field, id string, deps ...string) Rule {
	return Rule{
		ID:              id,
		Field:           field,
		Depends:         deps,
		SkipWhenMissing: true,
		Check: func(v Value) error {
			if _, ok := v.Raw.(int); !ok {
				return fmt.Errorf("expected int, got %T", v.Raw)
			}
			return nil
		},
	}
}

// IntRange fails when an int field falls outside [min, max].
func IntRange(field, id string, min, max int, deps ...string) Rule {
	return Rule{
		ID:              id,
		Field:           field,
		Depends:         deps,
		SkipWhenMissing: true,
		Check: func(v Value) error {
			n, _ := v.Raw.(int)
			if n < min || n > max {
				return fmt.Errorf("value %d outside range [%d, %d]", n, min, max)
			}
			return nil
		},
	}
}
