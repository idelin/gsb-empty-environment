package validate

import "fmt"

// Required fails when the field is absent.
func Required() CheckFunc {
	return func(_ interface{}, present bool) error {
		if !present {
			return fmt.Errorf("field is required")
		}
		return nil
	}
}

// Optional runs check only when the field is present; absent fields pass.
func Optional(check CheckFunc) CheckFunc {
	return func(value interface{}, present bool) error {
		if !present {
			return nil
		}
		return check(value, present)
	}
}

// IsString fails when the field is absent or not a string.
func IsString() CheckFunc {
	return func(value interface{}, present bool) error {
		if !present {
			return fmt.Errorf("missing field, expected string")
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
		return nil
	}
}

// IsBool fails when the field is absent or not a bool.
func IsBool() CheckFunc {
	return func(value interface{}, present bool) error {
		if !present {
			return fmt.Errorf("missing field, expected bool")
		}
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected bool, got %T", value)
		}
		return nil
	}
}

// IsInt fails when the field is absent or not an integer
// (int, int64, or a float64 with an integral value).
func IsInt() CheckFunc {
	return func(value interface{}, present bool) error {
		if !present {
			return fmt.Errorf("missing field, expected int")
		}
		if _, ok := asFloat(value, true); !ok {
			return fmt.Errorf("expected int, got %T", value)
		}
		return nil
	}
}

// IsNumber fails when the field is absent or not numeric.
func IsNumber() CheckFunc {
	return func(value interface{}, present bool) error {
		if !present {
			return fmt.Errorf("missing field, expected number")
		}
		if _, ok := asFloat(value, false); !ok {
			return fmt.Errorf("expected number, got %T", value)
		}
		return nil
	}
}

func asFloat(value interface{}, integral bool) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		if integral && v != float64(int64(v)) {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// Min fails when the field is not a number >= min.
func Min(min float64) CheckFunc {
	return func(value interface{}, present bool) error {
		f, ok := asFloat(value, false)
		if !present || !ok {
			return fmt.Errorf("expected number >= %v", min)
		}
		if f < min {
			return fmt.Errorf("must be >= %v", min)
		}
		return nil
	}
}

// Max fails when the field is not a number <= max.
func Max(max float64) CheckFunc {
	return func(value interface{}, present bool) error {
		f, ok := asFloat(value, false)
		if !present || !ok {
			return fmt.Errorf("expected number <= %v", max)
		}
		if f > max {
			return fmt.Errorf("must be <= %v", max)
		}
		return nil
	}
}

// LenMin fails when the field is not a string of at least min characters.
func LenMin(min int) CheckFunc {
	return func(value interface{}, present bool) error {
		s, ok := value.(string)
		if !present || !ok {
			return fmt.Errorf("expected string of length >= %d", min)
		}
		if len(s) < min {
			return fmt.Errorf("length must be >= %d", min)
		}
		return nil
	}
}

// LenMax fails when the field is not a string of at most max characters.
func LenMax(max int) CheckFunc {
	return func(value interface{}, present bool) error {
		s, ok := value.(string)
		if !present || !ok {
			return fmt.Errorf("expected string of length <= %d", max)
		}
		if len(s) > max {
			return fmt.Errorf("length must be <= %d", max)
		}
		return nil
	}
}

// OneOf fails when the field value does not equal any of the allowed values.
func OneOf(allowed ...interface{}) CheckFunc {
	return func(value interface{}, present bool) error {
		if !present {
			return fmt.Errorf("missing field, expected one of %v", allowed)
		}
		for _, a := range allowed {
			if value == a {
				return nil
			}
		}
		return fmt.Errorf("must be one of %v", allowed)
	}
}
