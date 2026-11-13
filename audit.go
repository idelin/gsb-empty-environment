package netsec

import (
	"strings"
)

type Level = string

const (
	LevelNone            Level = "None"
	LevelMetadata        Level = "Metadata"
	LevelRequest         Level = "Request"
	LevelRequestResponse Level = "RequestResponse"
)

type GroupResources struct {
	Group         string   `json:"group,omitempty"`
	Resources     []string `json:"resources,omitempty"`
	ResourceNames []string `json:"resourceNames,omitempty"`
}

type ResourceRule = GroupResources
type Resource = GroupResources
type ResourceGroup = GroupResources

type Rule struct {
	Level           Level            `json:"level"`
	Users           []string         `json:"users,omitempty"`
	UserGroups      []string         `json:"userGroups,omitempty"`
	Verbs           []string         `json:"verbs,omitempty"`
	Resources       []GroupResources `json:"resources,omitempty"`
	Namespaces      []string         `json:"namespaces,omitempty"`
	NonResourceURLs []string         `json:"nonResourceURLs,omitempty"`
}

type PolicyRule = Rule
type AuditRule = Rule

type Probe struct {
	User           string   `json:"user,omitempty"`
	UserName       string   `json:"userName,omitempty"`
	Username       string   `json:"username,omitempty"`
	UserGroups     []string `json:"userGroups,omitempty"`
	Groups         []string `json:"groups,omitempty"`
	Verb           string   `json:"verb,omitempty"`
	APIGroup       string   `json:"apiGroup,omitempty"`
	Group          string   `json:"group,omitempty"`
	ResourceGroup  string   `json:"resourceGroup,omitempty"`
	Resource       string   `json:"resource,omitempty"`
	Subresource    string   `json:"subresource,omitempty"`
	ResourceName   string   `json:"resourceName,omitempty"`
	Name           string   `json:"name,omitempty"`
	Namespace      string   `json:"namespace,omitempty"`
	NonResourceURL string   `json:"nonResourceURL,omitempty"`
	URL            string   `json:"url,omitempty"`
}

type Request = Probe
type AuditRequest = Probe

func Match(rule Rule, probe Probe) bool {
	return MatchRule(rule, probe)
}

func Resolve(rules []Rule, probe Probe) (Level, int) {
	for index := range rules {
		if MatchRule(rules[index], probe) {
			return rules[index].Level, index
		}
	}
	return LevelNone, -1
}

func Evaluate(rules []Rule, probe Probe) (Level, int) {
	return Resolve(rules, probe)
}

func FirstMatch(rules []Rule, probe Probe) (Level, int) {
	return Resolve(rules, probe)
}

func ResolveLevel(rules []Rule, probe Probe) Level {
	level, _ := Resolve(rules, probe)
	return level
}

func MatchRule(rule Rule, probe Probe) bool {
	request := normalizeProbe(probe)

	if !matchesAny(rule.Users, request.user) {
		return false
	}
	if !matchesGroups(rule.UserGroups, request.groups) {
		return false
	}
	if !matchesAny(rule.Verbs, request.verb) {
		return false
	}
	if !matchesAny(rule.Namespaces, request.namespace) {
		return false
	}

	if len(rule.Resources) == 0 && len(rule.NonResourceURLs) == 0 {
		return true
	}

	if request.nonResourceURL != "" {
		if len(rule.NonResourceURLs) == 0 {
			return false
		}
		return matchesURLs(rule.NonResourceURLs, request.nonResourceURL)
	}

	if len(rule.Resources) == 0 {
		return request.resource == "" && request.group == "" && request.resourceName == ""
	}
	if request.resource == "" && request.group == "" && request.resourceName == "" {
		return true
	}

	for _, groupResources := range rule.Resources {
		if matchesResource(groupResources, request) {
			return true
		}
	}
	return false
}

type normalizedProbe struct {
	user           string
	groups         []string
	verb           string
	group          string
	resource       string
	resourceName   string
	namespace      string
	nonResourceURL string
}

func normalizeProbe(probe Probe) normalizedProbe {
	user := firstNonEmpty(probe.User, probe.UserName, probe.Username)
	groups := appendUnique(nil, probe.UserGroups...)
	groups = appendUnique(groups, probe.Groups...)
	group := firstNonEmpty(probe.APIGroup, probe.Group, probe.ResourceGroup)
	resourceName := firstNonEmpty(probe.ResourceName, probe.Name)
	nonResourceURL := firstNonEmpty(probe.NonResourceURL, probe.URL)

	resource := probe.Resource
	if probe.Subresource != "" {
		switch {
		case resource == "":
			resource = probe.Subresource
		case !strings.Contains(resource, "/"):
			resource += "/" + probe.Subresource
		}
	}

	return normalizedProbe{
		user:           user,
		groups:         groups,
		verb:           probe.Verb,
		group:          group,
		resource:       resource,
		resourceName:   resourceName,
		namespace:      probe.Namespace,
		nonResourceURL: nonResourceURL,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func matchesAny(patterns []string, value string) bool {
	if len(patterns) == 0 || isWildcardList(patterns) || value == "" {
		return true
	}
	for _, pattern := range patterns {
		if pattern == "*" || pattern == value {
			return true
		}
	}
	return false
}

func matchesGroups(patterns []string, groups []string) bool {
	if len(patterns) == 0 || isWildcardList(patterns) || len(groups) == 0 {
		return true
	}
	for _, pattern := range patterns {
		for _, group := range groups {
			if pattern == "*" || pattern == group {
				return true
			}
		}
	}
	return false
}

func matchesResource(groupResources GroupResources, request normalizedProbe) bool {
	if !matchesGroup(groupResources.Group, request.group) {
		return false
	}
	if !matchesResourcePatterns(groupResources.Resources, request.resource) {
		return false
	}
	return matchesResourceNames(groupResources.ResourceNames, request.resourceName)
}

func matchesGroup(pattern, group string) bool {
	if pattern == "" || pattern == "*" || group == "" {
		return true
	}
	return pattern == group
}

func matchesResourcePatterns(patterns []string, resource string) bool {
	if len(patterns) == 0 || isWildcardList(patterns) || resource == "" {
		return true
	}
	for _, pattern := range patterns {
		if matchesResourcePattern(pattern, resource) {
			return true
		}
	}
	return false
}

func matchesResourceNames(names []string, name string) bool {
	if len(names) == 0 || isWildcardList(names) || name == "" {
		return true
	}
	for _, pattern := range names {
		if pattern == "*" || pattern == name {
			return true
		}
	}
	return false
}

func matchesResourcePattern(pattern, resource string) bool {
	if pattern == "" || pattern == resource || pattern == "*" {
		return true
	}
	patternParts := strings.Split(pattern, "/")
	resourceParts := strings.Split(resource, "/")
	if len(patternParts) != len(resourceParts) {
		return false
	}
	for index := range patternParts {
		if resourceParts[index] == "" {
			return false
		}
		if patternParts[index] != "*" && patternParts[index] != resourceParts[index] {
			return false
		}
	}
	return true
}

func matchesURLs(patterns []string, url string) bool {
	if len(patterns) == 0 || isWildcardList(patterns) || url == "" {
		return true
	}
	for _, pattern := range patterns {
		if matchesURLPattern(pattern, url) {
			return true
		}
	}
	return false
}

func matchesURLPattern(pattern, url string) bool {
	if pattern == "" || pattern == url || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(url, prefix+"/")
	}
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	urlParts := strings.Split(strings.TrimPrefix(url, "/"), "/")
	if len(patternParts) != len(urlParts) {
		return false
	}
	for index := range patternParts {
		if patternParts[index] != "*" && patternParts[index] != urlParts[index] {
			return false
		}
	}
	return true
}

func isWildcardList(values []string) bool {
	for _, value := range values {
		if value == "*" {
			return true
		}
	}
	return false
}

func appendUnique(target []string, values ...string) []string {
	for _, value := range values {
		found := false
		for _, existing := range target {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			target = append(target, value)
		}
	}
	return target
}
