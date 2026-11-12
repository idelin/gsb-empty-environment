package netsec

import (
	"errors"
	"strings"
)

// Severity of a policy violation.
type Severity string

const (
	SeverityFAIL Severity = "FAIL"
	SeverityWARN Severity = "WARN"
	SeverityINFO Severity = "INFO"
)

// Aggregation operator across a policy's conditions.
type Operator string

const (
	OpALL Operator = "ALL"
	OpANY Operator = "ANY"
)

// Conditionable purl fields.
const (
	FieldType      = "type"
	FieldNamespace = "namespace"
	FieldName      = "name"
	FieldVersion   = "version"
	FieldSubpath   = "subpath"
)

// Condition matches one purl field against a glob pattern. Qualifier
// fields use the "qualifiers:<key>" form. "*" matches any number of
// characters, including none; all other bytes match literally.
type Condition struct {
	Field   string
	Pattern string
}

// Policy is a set of conditions aggregated with OpALL or OpANY.
type Policy struct {
	ID         string
	Severity   Severity
	Conditions []Condition
	Operator   Operator
}

// Finding reports a component's highest-severity policy hit.
type Finding struct {
	PolicyID string
	Severity Severity
}

// Match reports whether the purl satisfies every condition (ALL) or any
// condition (ANY). Subpath is available as a field but is excluded by
// simply not referencing it in policy conditions.
func (p Policy) Match(purl *PURL) (bool, error) {
	if err := p.Validate(); err != nil {
		return false, err
	}
	if len(p.Conditions) == 0 {
		return false, nil
	}

	switch p.Operator {
	case OpALL:
		for _, condition := range p.Conditions {
			matched, err := condition.match(purl)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	case OpANY:
		for _, condition := range p.Conditions {
			matched, err := condition.match(purl)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, errors.New("policy: unknown operator " + string(p.Operator))
	}
}

// Validate checks severity, operator and condition field names.
func (p Policy) Validate() error {
	switch p.Severity {
	case SeverityFAIL, SeverityWARN, SeverityINFO:
	default:
		return errors.New("policy: invalid severity " + string(p.Severity))
	}
	switch p.Operator {
	case OpALL, OpANY:
	default:
		return errors.New("policy: operator must be ALL or ANY")
	}
	for _, condition := range p.Conditions {
		if err := validateField(condition.Field); err != nil {
			return err
		}
		if condition.Pattern == "" {
			return errors.New("policy: condition pattern must not be empty")
		}
	}
	return nil
}

func (c Condition) match(purl *PURL) (bool, error) {
	value, err := fieldValue(purl, c.Field)
	if err != nil {
		return false, err
	}
	pattern, err := decodeComponent(c.Pattern)
	if err != nil {
		return false, err
	}
	return globMatch(pattern, value), nil
}

func fieldValue(purl *PURL, field string) (string, error) {
	if qualifierKey, ok := strings.CutPrefix(field, "qualifiers:"); ok {
		if qualifierKey == "" {
			return "", errors.New("policy: qualifier field requires a key")
		}
		return purl.Qualifiers[strings.ToLower(qualifierKey)], nil
	}
	switch field {
	case FieldType:
		return purl.Type, nil
	case FieldNamespace:
		return purl.Namespace, nil
	case FieldName:
		return purl.Name, nil
	case FieldVersion:
		return purl.Version, nil
	case FieldSubpath:
		return purl.Subpath, nil
	default:
		return "", errors.New("policy: unknown condition field " + field)
	}
}

func validateField(field string) error {
	if strings.HasPrefix(field, "qualifiers:") {
		if len(field) == len("qualifiers:") {
			return errors.New("policy: qualifier field requires a key")
		}
		return nil
	}
	switch field {
	case FieldType, FieldNamespace, FieldName, FieldVersion, FieldSubpath:
		return nil
	default:
		return errors.New("policy: unknown condition field " + field)
	}
}

// globMatch supports "*" as the only wildcard, matching any run of bytes
// (including the empty run).
func globMatch(pattern, value string) bool {
	star := strings.IndexByte(pattern, '*')
	if star < 0 {
		return pattern == value
	}
	if len(value) < star || value[:star] != pattern[:star] {
		return false
	}
	suffix := pattern[star+1:]
	if len(suffix) > len(value) || value[len(value)-len(suffix):] != suffix {
		return false
	}
	return true
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityFAIL:
		return 3
	case SeverityWARN:
		return 2
	case SeverityINFO:
		return 1
	default:
		return 0
	}
}

// Evaluate runs all policies against the parsed purl and returns the
// highest-severity hit. A nil finding means no policy matched.
func Evaluate(purl *PURL, policies []Policy) (*Finding, error) {
	var best *Finding
	for _, policy := range policies {
		matched, err := policy.Match(purl)
		if err != nil {
			return nil, err
		}
		if matched && (best == nil || severityRank(policy.Severity) > severityRank(best.Severity)) {
			best = &Finding{PolicyID: policy.ID, Severity: policy.Severity}
		}
	}
	return best, nil
}

// CheckComponent is the SBOM ingestion entry point: it parses and
// normalizes the component purl, then evaluates the policies.
func CheckComponent(purl string, policies []Policy) (*Finding, error) {
	parsed, err := Parse(purl)
	if err != nil {
		return nil, err
	}
	parsed.Normalize()
	return Evaluate(parsed, policies)
}
