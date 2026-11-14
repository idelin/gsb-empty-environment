package netsec

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type conditionOperator struct {
	name       string
	quantifier string
	ifExists   bool
}

func parseConditionOperator(raw string) (name string, quantifier string, ifExists bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false, fmt.Errorf("empty condition operator")
	}
	if i := strings.Index(raw, ":"); i >= 0 {
		quantifier = strings.TrimSpace(raw[:i])
		name = strings.TrimSpace(raw[i+1:])
		if quantifier != "ForAnyValue" && quantifier != "ForAllValues" {
			return "", "", false, fmt.Errorf("unknown condition quantifier %q", quantifier)
		}
	} else {
		name = raw
	}
	if strings.HasSuffix(name, "IfExists") {
		ifExists = true
		name = strings.TrimSuffix(name, "IfExists")
	}
	switch name {
	case "StringEquals", "StringNotEquals", "StringLike", "StringNotLike",
		"ArnEquals", "ArnNotEquals", "ArnLike", "ArnNotLike",
		"NumericEquals", "NumericNotEquals", "NumericLessThan",
		"NumericLessThanEquals", "NumericGreaterThan", "NumericGreaterThanEquals",
		"Bool", "IpAddress", "NotIpAddress", "Null":
		return name, quantifier, ifExists, nil
	default:
		return "", "", false, fmt.Errorf("unknown condition operator %q", raw)
	}
}

func evaluateConditions(block ConditionBlock, values ConditionValues) (bool, error) {
	for rawOperator, entries := range block {
		name, quantifier, ifExists, err := parseConditionOperator(rawOperator)
		if err != nil {
			return false, err
		}
		for key, policyValues := range entries {
			requestValues := values[key]
			present := len(requestValues) > 0

			if name == "Null" {
				ok, err := evaluateNull(policyValues, present, requestValues)
				if err != nil {
					return false, err
				}
				if !ok {
					return false, nil
				}
				continue
			}
			if !present {
				if ifExists {
					continue
				}
				return false, nil
			}
			ok, err := evaluateConditionKey(name, quantifier, requestValues, policyValues)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
	}
	return true, nil
}

func evaluateConditionKey(name, quantifier string, requestValues []string, policyValues StringList) (bool, error) {
	if len(policyValues) == 0 {
		return false, nil
	}
	switch quantifier {
	case "ForAllValues":
		for _, requestValue := range requestValues {
			ok := matchesAnyPolicyValue(name, requestValue, policyValues)
			if !ok {
				return false, nil
			}
		}
		return true, nil
	default:
		for _, requestValue := range requestValues {
			if matchesAnyPolicyValue(name, requestValue, policyValues) {
				return true, nil
			}
		}
		return false, nil
	}
}

func matchesAnyPolicyValue(name string, requestValue string, policyValues StringList) bool {
	switch name {
	case "StringEquals", "ArnEquals":
		for _, policyValue := range policyValues {
			if requestValue == string(policyValue) {
				return true
			}
		}
	case "StringNotEquals", "ArnNotEquals":
		for _, policyValue := range policyValues {
			if requestValue == string(policyValue) {
				return false
			}
		}
		return true
	case "StringLike", "ArnLike":
		for _, policyValue := range policyValues {
			if globMatch(string(policyValue), requestValue) {
				return true
			}
		}
	case "StringNotLike", "ArnNotLike":
		for _, policyValue := range policyValues {
			if globMatch(string(policyValue), requestValue) {
				return false
			}
		}
		return true
	case "NumericEquals", "NumericNotEquals", "NumericLessThan",
		"NumericLessThanEquals", "NumericGreaterThan", "NumericGreaterThanEquals":
		requestNumber, err := strconv.ParseFloat(strings.TrimSpace(requestValue), 64)
		if err != nil {
			return false
		}
		if name == "NumericNotEquals" {
			for _, policyValue := range policyValues {
				policyNumber, err := strconv.ParseFloat(strings.TrimSpace(string(policyValue)), 64)
				if err != nil {
					continue
				}
				if requestNumber == policyNumber {
					return false
				}
			}
			return true
		}
		for _, policyValue := range policyValues {
			policyNumber, err := strconv.ParseFloat(strings.TrimSpace(string(policyValue)), 64)
			if err != nil {
				continue
			}
			if numericConditionMatches(name, requestNumber, policyNumber) {
				return true
			}
		}
	case "Bool":
		requestBool, err := strconv.ParseBool(strings.TrimSpace(requestValue))
		if err != nil {
			return false
		}
		for _, policyValue := range policyValues {
			policyBool, err := strconv.ParseBool(strings.TrimSpace(string(policyValue)))
			if err != nil {
				continue
			}
			if requestBool == policyBool {
				return true
			}
		}
	case "IpAddress":
		requestIP := net.ParseIP(strings.TrimSpace(requestValue))
		if requestIP == nil {
			return false
		}
		for _, policyValue := range policyValues {
			network, single, err := parseCIDROrIP(strings.TrimSpace(string(policyValue)))
			if err != nil {
				continue
			}
			if network != nil && network.Contains(requestIP) {
				return true
			}
			if single != nil && single.Equal(requestIP) {
				return true
			}
		}
	case "NotIpAddress":
		requestIP := net.ParseIP(strings.TrimSpace(requestValue))
		if requestIP == nil {
			return false
		}
		for _, policyValue := range policyValues {
			network, single, err := parseCIDROrIP(strings.TrimSpace(string(policyValue)))
			if err != nil {
				continue
			}
			if (network != nil && network.Contains(requestIP)) || (single != nil && single.Equal(requestIP)) {
				return false
			}
		}
		return true
	}
	return false
}

func numericConditionMatches(operator string, requestNumber, policyNumber float64) bool {
	switch operator {
	case "NumericEquals":
		return requestNumber == policyNumber
	case "NumericNotEquals":
		return requestNumber != policyNumber
	case "NumericLessThan":
		return requestNumber < policyNumber
	case "NumericLessThanEquals":
		return requestNumber <= policyNumber
	case "NumericGreaterThan":
		return requestNumber > policyNumber
	case "NumericGreaterThanEquals":
		return requestNumber >= policyNumber
	default:
		return false
	}
}

func evaluateNull(policyValues StringList, present bool, requestValues []string) (bool, error) {
	effectivePresent := present
	if effectivePresent {
		allEmpty := true
		for _, value := range requestValues {
			if value != "" {
				allEmpty = false
				break
			}
		}
		effectivePresent = !allEmpty
	}
	for _, policyValue := range policyValues {
		want, err := strconv.ParseBool(strings.TrimSpace(string(policyValue)))
		if err != nil {
			return false, err
		}
		if want == !effectivePresent {
			return true, nil
		}
	}
	return false, nil
}

func parseCIDROrIP(value string) (*net.IPNet, net.IP, error) {
	if value == "" {
		return nil, nil, fmt.Errorf("empty IP value")
	}
	if strings.Contains(value, "/") {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid CIDR %q: %w", value, err)
		}
		return network, nil, nil
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return nil, nil, fmt.Errorf("invalid IP %q", value)
	}
	return nil, ip, nil
}
