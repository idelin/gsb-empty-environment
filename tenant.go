package netsec

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// NodeIDsFromArguments returns string values held in ID-like argument keys.
// It walks lists and input objects without using reflection or map keys as
// object property paths.
func NodeIDsFromArguments(arguments map[string]any) []string {
	var result []string
	seen := make(map[string]struct{})
	var walk func(key string, value any)
	walk = func(key string, value any) {
		if isIDKey(key) {
			switch typed := value.(type) {
			case string:
				addUniqueString(&result, seen, typed)
				return
			case []string:
				for _, item := range typed {
					addUniqueString(&result, seen, item)
				}
				return
			case []any:
				for _, item := range typed {
					walk(key, item)
				}
				return
			case map[string]any:
				// Input objects keyed by ID are uncommon, but preserve all
				// scalar leaves rather than coercing a map to a string.
				for childKey, childValue := range typed {
					walk(childKey, childValue)
				}
				return
			}
		}
		switch typed := value.(type) {
		case map[string]any:
			for childKey, childValue := range typed {
				walk(childKey, childValue)
			}
		case []any:
			for _, item := range typed {
				walk(key, item)
			}
		}
	}
	for key, value := range arguments {
		walk(key, value)
	}
	sort.Strings(result)
	return result
}

func addUniqueString(result *[]string, seen map[string]struct{}, value string) {
	if value == "" {
		return
	}
	if _, exists := seen[value]; exists {
		return
	}
	seen[value] = struct{}{}
	*result = append(*result, value)
}

func normalizeArgumentName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "")
	name = strings.ReplaceAll(name, "-", "")
	return name
}

func isIDKey(name string) bool {
	normalized := normalizeArgumentName(name)
	return normalized == "id" ||
		normalized == "ids" ||
		normalized == "nodeid" ||
		normalized == "nodeids" ||
		normalized == "userid" ||
		normalized == "userids" ||
		normalized == "accountid" ||
		normalized == "accountids" ||
		normalized == "tenantid" ||
		normalized == "tenantids"
}

// TenantFromNodeID extracts an explicitly scoped tenant from common opaque node
// ID formats. Supported examples include tenant-a:User:42,
// gid://tenant-a/User/42, tenant-a/User/42, and base64 encodings of those
// values. Unscoped IDs such as User:42 return false.
func TenantFromNodeID(raw string) (string, bool) {
	candidates := []string{strings.TrimSpace(raw)}
	for depth := 0; depth < 2; depth++ {
		current := append([]string(nil), candidates...)
		for _, candidate := range current {
			for _, decoded := range decodeNodeIDCandidate(candidate) {
				if decoded != candidate {
					candidates = append(candidates, decoded)
				}
			}
		}
	}
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if tenant, ok := tenantFromScopedString(candidate); ok {
			return tenant, true
		}
	}
	return "", false
}

func decodeNodeIDCandidate(candidate string) []string {
	var result []string
	trimmed := candidate
	if strings.HasPrefix(trimmed, "gid://") {
		trimmed = strings.TrimPrefix(trimmed, "gid://")
		parts := splitScoped(trimmed)
		if len(parts) >= 3 {
			// gid://tenant-a/User/42 -> tenant-a is the first segment.
		}
	}
	if decoded, err := url.PathUnescape(candidate); err == nil && decoded != candidate {
		result = append(result, decoded)
	}
	for _, encoding := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		decoded, err := encoding.DecodeString(candidate)
		if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) {
			continue
		}
		text := string(decoded)
		if isMostlyPrintable(text) {
			result = append(result, text)
		}
	}
	return result
}

func isMostlyPrintable(value string) bool {
	if value == "" {
		return false
	}
	printable := 0
	for _, r := range value {
		if r >= 0x20 && r != 0x7f {
			printable++
		}
	}
	return printable == utf8.RuneCountInString(value)
}

func splitScoped(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ':' || r == '/' || r == '|' || r == '\\'
	})
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			result = append(result, field)
		}
	}
	return result
}

func tenantFromScopedString(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "{") {
		var object map[string]any
		if err := json.Unmarshal([]byte(value), &object); err == nil {
			for _, key := range []string{"tenant_id", "tenantId", "tenant", "tenantID"} {
				if tenant, ok := object[key].(string); ok && tenant != "" {
					return tenant, true
				}
			}
		}
	}
	value = strings.TrimPrefix(value, "gid://")
	parts := splitScoped(value)
	if len(parts) == 0 {
		return "", false
	}
	if normalizeArgumentName(parts[0]) == "tenant" && len(parts) >= 4 {
		return parts[1], true
	}
	if len(parts) >= 3 {
		first := parts[0]
		if first == "" || strings.EqualFold(first, "gid") {
			return "", false
		}
		return first, true
	}
	if len(parts) == 2 {
		first := normalizeArgumentName(parts[0])
		if strings.HasPrefix(first, "tenant") || strings.HasPrefix(first, "tnt") || strings.HasPrefix(first, "org") {
			return parts[0], true
		}
	}
	return "", false
}

func tenantArgumentMismatch(arguments map[string]any, tenantID string) []string {
	if tenantID == "" {
		return nil
	}
	var mismatches []string
	var walk func(key string, value any)
	walk = func(key string, value any) {
		normalized := normalizeArgumentName(key)
		switch typed := value.(type) {
		case string:
			if normalized == "tenantid" {
				if typed != tenantID {
					mismatches = append(mismatches, typed)
				}
				return
			}
			if isIDKey(key) {
				if tenant, ok := TenantFromNodeID(typed); ok && tenant != tenantID {
					mismatches = append(mismatches, typed)
				}
			}
		case []string:
			for _, item := range typed {
				walk(key, item)
			}
		case []any:
			for _, item := range typed {
				walk(key, item)
			}
		case map[string]any:
			for childKey, childValue := range typed {
				walk(childKey, childValue)
			}
		}
	}
	for key, value := range arguments {
		walk(key, value)
	}
	sort.Strings(mismatches)
	return mismatches
}

// TenantNodePolicy composes a base policy with a cross-tenant node-ID guard.
// A nil base policy allows all fields before the node-ID check.
type TenantNodePolicy struct {
	Inner    Policy
	TenantOf func(string) (string, bool)
}

// NewTenantNodePolicy creates a cross-tenant node-ID policy. If inner is nil,
// every field is otherwise allowed.
func NewTenantNodePolicy(inner Policy) *TenantNodePolicy {
	return &TenantNodePolicy{Inner: inner, TenantOf: TenantFromNodeID}
}

// AuthorizeField implements Policy.
func (p *TenantNodePolicy) AuthorizeField(ctx Context, request FieldRequest) (Decision, error) {
	decision := Allow()
	var err error
	if p != nil && p.Inner != nil {
		decision, err = p.Inner.AuthorizeField(ctx, request)
		if err != nil {
			return Decision{}, err
		}
	}
	if !decision.Allowed {
		return decision, nil
	}
	resolver := TenantFromNodeID
	if p != nil && p.TenantOf != nil {
		resolver = p.TenantOf
	}
	for _, nodeID := range NodeIDsFromArguments(request.Arguments) {
		tenant, ok := resolver(nodeID)
		if ok && (request.Identity.TenantID == "" || tenant != request.Identity.TenantID) {
			decision.Allowed = false
			decision.Reason = "cross-tenant node ID"
			return decision, nil
		}
	}
	if request.Identity.TenantID == "" {
		return decision, nil
	}
	for _, mismatch := range tenantArgumentMismatch(request.Arguments, request.Identity.TenantID) {
		decision.Allowed = false
		decision.Reason = "cross-tenant argument " + mismatch
		return decision, nil
	}
	return decision, nil
}
