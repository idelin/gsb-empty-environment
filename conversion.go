package netsec

import (
	"errors"
	"fmt"
)

// FieldMapping explicitly maps one source field number to one target field
// number. Conversion never copies a field unless a FieldMapping names it,
// so a number reused with different meaning across versions cannot silently
// change semantics.
type FieldMapping struct {
	From int
	To   int
	// Transform, when non-nil, converts a source value to the target type.
	// When nil the source and target must share the same FieldType.
	Transform func(any) (any, error)
}

// ConversionPlan is a validated, explicit field mapping between two versions.
type ConversionPlan struct {
	registry *Registry
	from     string
	to       string
	mappings map[int]FieldMapping
}

// NewConversionPlan compiles and validates an explicit mapping between two
// registered versions. It rejects unknown or reserved endpoints, duplicate
// targets, and type changes without a Transform.
func (r *Registry) NewConversionPlan(from, to string, mappings []FieldMapping) (*ConversionPlan, error) {
	src, ok := r.Schema(from)
	if !ok {
		return nil, fmt.Errorf("netsec: unknown source version %q", from)
	}
	dst, ok := r.Schema(to)
	if !ok {
		return nil, fmt.Errorf("netsec: unknown target version %q", to)
	}

	plan := &ConversionPlan{
		registry: r,
		from:     from,
		to:       to,
		mappings: make(map[int]FieldMapping, len(mappings)),
	}
	for _, m := range mappings {
		srcSpec, ok := src.Field(m.From)
		if !ok {
			return nil, fmt.Errorf("netsec: mapping source field %d is not known in version %q", m.From, from)
		}
		if src.IsReserved(m.From) {
			return nil, fmt.Errorf("netsec: mapping source field %d is reserved in version %q", m.From, from)
		}
		dstSpec, ok := dst.Field(m.To)
		if !ok {
			return nil, fmt.Errorf("netsec: mapping target field %d is not known in version %q", m.To, to)
		}
		if dst.IsReserved(m.To) {
			return nil, fmt.Errorf("netsec: mapping target field %d is reserved in version %q", m.To, to)
		}
		if _, dup := plan.mappings[m.From]; dup {
			return nil, fmt.Errorf("netsec: duplicate mapping from source field %d", m.From)
		}
		for _, existing := range plan.mappings {
			if existing.To == m.To {
				return nil, fmt.Errorf("netsec: fields %d and %d both map to target field %d", existing.From, m.From, m.To)
			}
		}
		if m.Transform == nil && srcSpec.Type != dstSpec.Type {
			return nil, fmt.Errorf(
				"netsec: field %d (%s) to field %d (%s) changes type without a Transform",
				m.From, srcSpec.Type, m.To, dstSpec.Type,
			)
		}
		plan.mappings[m.From] = m
	}
	return plan, nil
}

// From returns the source version identifier.
func (p *ConversionPlan) From() string { return p.from }

// To returns the target version identifier.
func (p *ConversionPlan) To() string { return p.to }

// ConversionResult reports what happened during a conversion.
type ConversionResult struct {
	// Mapped counts source occurrences carried into the target message.
	Mapped int
	// Unmapped counts source occurrences with no explicit mapping; these are
	// never forwarded, closing the unknown-field forwarding hole.
	Unmapped int
}

// Convert applies the explicit plan to an already-sanitized source message
// and returns a raw target message ready for target-version sanitization.
// Only mapped known fields are emitted, in ascending source-number order.
func (p *ConversionPlan) Convert(src *SanitizedMessage) (RawMessage, ConversionResult, error) {
	if src == nil {
		return nil, ConversionResult{}, errors.New("netsec: cannot convert nil message")
	}
	if src.Version() != p.from {
		return nil, ConversionResult{}, fmt.Errorf(
			"netsec: message version %q does not match plan source %q", src.Version(), p.from,
		)
	}
	dst, ok := p.registry.Schema(p.to)
	if !ok {
		return nil, ConversionResult{}, fmt.Errorf("netsec: target version %q no longer registered", p.to)
	}

	result := ConversionResult{}
	out := make(RawMessage, 0)
	for _, number := range src.Numbers() {
		values := src.Get(number)
		mapping, mapped := p.mappings[number]
		if !mapped {
			result.Unmapped += len(values)
			continue
		}
		dstSpec, _ := dst.Field(mapping.To)
		for _, v := range values {
			converted := v
			if mapping.Transform != nil {
				c, err := mapping.Transform(v)
				if err != nil {
					return nil, result, fmt.Errorf("netsec: transform for field %d failed: %w", number, err)
				}
				converted = c
			}
			if !matchesType(dstSpec.Type, converted) {
				return nil, result, fmt.Errorf(
					"netsec: transform produced %T for target field %d (declared %s)",
					converted, mapping.To, dstSpec.Type,
				)
			}
			out = append(out, RawField{Number: mapping.To, Value: converted})
			result.Mapped++
		}
	}
	return out, result, nil
}
