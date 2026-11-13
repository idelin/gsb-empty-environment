package netsec

import (
	"strconv"
	"strings"
)

const shadowCandidateLimit = 200000

func ruleIsShadowed(rules []Rule, index int) bool {
	target := rules[index]
	prior := rules[:index]

	for priorIndex := range prior {
		if ruleSubsumes(prior[priorIndex], target) {
			return true
		}
	}

	hasResources := len(target.Resources) > 0
	hasURLs := len(target.NonResourceURLs) > 0

	if !hasResources && !hasURLs {
		if !resourceEntryCovered(target, GroupResources{}, prior, rules) {
			return false
		}
		if !urlPatternCovered(target, "*", prior, rules) {
			return false
		}
		return true
	}

	if hasResources {
		for _, entry := range target.Resources {
			if !resourceEntryCovered(target, entry, prior, rules) {
				return false
			}
		}
	}
	if hasURLs {
		for _, pattern := range target.NonResourceURLs {
			if !urlPatternCovered(target, pattern, prior, rules) {
				return false
			}
		}
	}
	return true
}

func ruleSubsumes(covering, target Rule) bool {
	coveringResources := ruleHasResourceScope(covering)
	coveringURLs := ruleHasURLScope(covering)
	targetResources := ruleHasResourceScope(target)
	targetURLs := ruleHasURLScope(target)

	if targetResources && !coveringResources {
		return false
	}
	if targetURLs && !coveringURLs {
		return false
	}
	if !valuesCover(covering.Users, target.Users) {
		return false
	}
	if !groupsCover(covering.UserGroups, target.UserGroups) {
		return false
	}
	if !valuesCover(covering.Verbs, target.Verbs) {
		return false
	}
	if !valuesCover(covering.Namespaces, target.Namespaces) {
		return false
	}
	if targetResources && len(covering.Resources) > 0 && !resourceEntriesCover(covering.Resources, target.Resources) {
		return false
	}
	if targetURLs && len(covering.NonResourceURLs) > 0 && !urlPatternsCover(covering.NonResourceURLs, target.NonResourceURLs) {
		return false
	}
	return true
}

func ruleHasResourceScope(rule Rule) bool {
	return len(rule.Resources) > 0 || (len(rule.Resources) == 0 && len(rule.NonResourceURLs) == 0)
}

func ruleHasURLScope(rule Rule) bool {
	return len(rule.NonResourceURLs) > 0 || (len(rule.Resources) == 0 && len(rule.NonResourceURLs) == 0)
}

func valuesCover(covering, target []string) bool {
	if len(covering) == 0 || isWildcardList(covering) {
		return true
	}
	if len(target) == 0 || isWildcardList(target) {
		return false
	}
	for _, value := range target {
		if !containsValue(covering, value) {
			return false
		}
	}
	return true
}

func groupsCover(covering, target []string) bool {
	if len(covering) == 0 || isWildcardList(covering) {
		return true
	}
	if len(target) == 0 || isWildcardList(target) {
		return false
	}
	for _, group := range target {
		if !containsValue(covering, group) {
			return false
		}
	}
	return true
}

func containsValue(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == "*" || candidate == value {
			return true
		}
	}
	return false
}

func resourceEntriesCover(covering, target []GroupResources) bool {
	if len(target) == 0 {
		return true
	}
	for _, targetEntry := range target {
		covered := false
		for _, coveringEntry := range covering {
			if resourceEntrySubsumes(coveringEntry, targetEntry) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func resourceEntrySubsumes(covering, target GroupResources) bool {
	if !groupCovers(covering.Group, target.Group) {
		return false
	}
	if !resourcePatternsCover(covering.Resources, target.Resources) {
		return false
	}
	return valuesCover(covering.ResourceNames, target.ResourceNames)
}

func groupCovers(covering, target string) bool {
	if covering == "" || covering == "*" {
		return true
	}
	if target == "" || target == "*" {
		return false
	}
	return covering == target
}

func resourcePatternsCover(covering, target []string) bool {
	if len(covering) == 0 || isWildcardList(covering) {
		return true
	}
	if len(target) == 0 || isWildcardList(target) {
		return false
	}
	for _, targetPattern := range target {
		found := false
		for _, coveringPattern := range covering {
			if resourcePatternCovers(coveringPattern, targetPattern) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func resourcePatternCovers(covering, target string) bool {
	if covering == "*" {
		return true
	}
	if target == "*" || covering == "" {
		return false
	}
	if covering == target {
		return true
	}
	coveringParts := strings.Split(covering, "/")
	targetParts := strings.Split(target, "/")
	if len(coveringParts) != len(targetParts) {
		return false
	}
	for index := range coveringParts {
		if coveringParts[index] != "*" && coveringParts[index] != targetParts[index] {
			return false
		}
		if targetParts[index] == "" {
			return false
		}
	}
	return true
}

func urlPatternsCover(covering, target []string) bool {
	if len(covering) == 0 || isWildcardList(covering) {
		return true
	}
	if len(target) == 0 || isWildcardList(target) {
		return false
	}
	for _, targetPattern := range target {
		found := false
		for _, coveringPattern := range covering {
			if urlPatternCovers(coveringPattern, targetPattern) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func urlPatternCovers(covering, target string) bool {
	if covering == "*" {
		return true
	}
	if target == "*" || covering == "" {
		return false
	}
	if covering == target {
		return true
	}
	if strings.HasSuffix(covering, "/*") {
		prefix := strings.TrimSuffix(covering, "/*")
		if strings.HasSuffix(target, "/*") {
			return strings.TrimSuffix(target, "/*") == prefix
		}
		return strings.HasPrefix(target, prefix+"/")
	}
	coveringParts := strings.Split(strings.TrimPrefix(covering, "/"), "/")
	targetParts := strings.Split(strings.TrimPrefix(target, "/"), "/")
	if len(coveringParts) != len(targetParts) {
		return false
	}
	for index := range coveringParts {
		if coveringParts[index] != "*" && coveringParts[index] != targetParts[index] {
			return false
		}
	}
	return true
}

type shadowCandidate struct {
	users      []string
	groups     [][]string
	verbs      []string
	namespaces []string
}

func resourceEntryCovered(target Rule, entry GroupResources, prior []Rule, all []Rule) bool {
	scalars := shadowScalars(target, all)
	resources := resourceCandidates(entry, all)
	groups := groupResourceCandidates(entry.Group, all)
	names := resourceNameCandidates(entry.ResourceNames, all)

	if len(resources) == 0 || len(groups) == 0 || len(names) == 0 {
		return false
	}

	checked := 0
	for _, user := range scalars.users {
		for _, userGroups := range scalars.groups {
			for _, verb := range scalars.verbs {
				for _, namespace := range scalars.namespaces {
					for _, group := range groups {
						for _, resource := range resources {
							for _, name := range names {
								if checked >= shadowCandidateLimit {
									return false
								}
								checked++
								probe := Probe{
									User:         user,
									UserGroups:   userGroups,
									Verb:         verb,
									APIGroup:     group,
									Resource:     resource,
									ResourceName: name,
									Namespace:    namespace,
								}
								if !MatchRule(target, probe) {
									continue
								}
								if !matchesPriorRule(prior, probe) {
									return false
								}
							}
						}
					}
				}
			}
		}
	}
	return checked > 0
}

func urlPatternCovered(target Rule, pattern string, prior []Rule, all []Rule) bool {
	scalars := shadowScalars(target, all)
	urls := urlCandidates(pattern, all)
	if len(urls) == 0 {
		return false
	}

	checked := 0
	for _, user := range scalars.users {
		for _, userGroups := range scalars.groups {
			for _, verb := range scalars.verbs {
				for _, namespace := range scalars.namespaces {
					for _, url := range urls {
						if checked >= shadowCandidateLimit {
							return false
						}
						checked++
						probe := Probe{
							User:           user,
							UserGroups:     userGroups,
							Verb:           verb,
							Namespace:      namespace,
							NonResourceURL: url,
						}
						if !MatchRule(target, probe) {
							continue
						}
						if !matchesPriorRule(prior, probe) {
							return false
						}
					}
				}
			}
		}
	}
	return checked > 0
}

func matchesPriorRule(prior []Rule, probe Probe) bool {
	for index := range prior {
		if MatchRule(prior[index], probe) {
			return true
		}
	}
	return false
}

func shadowScalars(target Rule, all []Rule) shadowCandidate {
	allUsers := collectRuleValues(all, func(rule Rule) []string { return rule.Users })
	allGroups := collectRuleValues(all, func(rule Rule) []string { return rule.UserGroups })
	allVerbs := collectRuleValues(all, func(rule Rule) []string { return rule.Verbs })
	allNamespaces := collectRuleValues(all, func(rule Rule) []string { return rule.Namespaces })

	return shadowCandidate{
		users:      orderedCandidates(target.Users, allUsers, "shadow-user"),
		groups:     groupCandidates(target.UserGroups, allGroups, "shadow-group"),
		verbs:      orderedCandidates(target.Verbs, allVerbs, "shadow-verb"),
		namespaces: orderedCandidates(target.Namespaces, allNamespaces, "shadow-namespace"),
	}
}

func collectRuleValues(rules []Rule, getter func(Rule) []string) []string {
	var values []string
	for _, rule := range rules {
		for _, value := range getter(rule) {
			if value != "*" {
				values = appendUnique(values, value)
			}
		}
	}
	return values
}

func orderedCandidates(restricted, all []string, freshBase string) []string {
	if len(restricted) > 0 && !isWildcardList(restricted) {
		return appendUnique(nil, restricted...)
	}
	values := appendUnique(nil, all...)
	return appendUnique(values, freshValue(freshBase, values))
}

func groupCandidates(restricted, all []string, freshBase string) [][]string {
	if len(restricted) > 0 && !isWildcardList(restricted) {
		values := make([][]string, 0, len(restricted))
		for _, group := range restricted {
			values = append(values, []string{group})
		}
		return values
	}
	fresh := freshValue(freshBase, all)
	values := [][]string{{}}
	for _, group := range all {
		values = append(values, []string{group})
	}
	values = append(values, []string{fresh})
	return values
}

func groupResourceCandidates(restricted string, all []Rule) []string {
	allGroups := collectRuleValues(all, func(rule Rule) []string {
		var groups []string
		for _, entry := range rule.Resources {
			if entry.Group != "" && entry.Group != "*" {
				groups = appendUnique(groups, entry.Group)
			}
		}
		return groups
	})
	if restricted != "" && restricted != "*" {
		return []string{restricted}
	}
	values := appendUnique(nil, allGroups...)
	values = appendUnique(values, "", freshValue("shadow-group-resource", values))
	return values
}

func resourceNameCandidates(restricted []string, all []Rule) []string {
	allNames := collectRuleValues(all, func(rule Rule) []string {
		var names []string
		for _, entry := range rule.Resources {
			names = appendUnique(names, entry.ResourceNames...)
		}
		return names
	})
	return orderedCandidates(restricted, allNames, "shadow-resource-name")
}

func resourceCandidates(entry GroupResources, all []Rule) []string {
	examples := collectResourceExamples(all)
	if len(entry.Resources) == 0 || isWildcardList(entry.Resources) {
		values := appendUnique(nil, examples...)
		return appendUnique(values, freshValue("shadow-resource", values))
	}

	var values []string
	for _, pattern := range entry.Resources {
		for _, example := range instantiatePattern(pattern) {
			if matchesResourcePattern(pattern, example) {
				values = appendUnique(values, example)
			}
		}
		for _, example := range examples {
			if matchesResourcePattern(pattern, example) {
				values = appendUnique(values, example)
			}
		}
	}
	return values
}

func collectResourceExamples(rules []Rule) []string {
	values := []string{
		"example-widget",
		"secrets",
		"configmaps",
		"tokenreviews",
		"serviceaccounts/token",
	}
	for _, rule := range rules {
		for _, entry := range rule.Resources {
			for _, pattern := range entry.Resources {
				for _, example := range instantiatePattern(pattern) {
					values = appendUnique(values, example)
				}
			}
		}
	}
	return values
}

func instantiatePattern(pattern string) []string {
	if pattern == "" {
		return nil
	}
	if pattern == "*" {
		return []string{"example-widget", "example-widget/status"}
	}
	parts := strings.Split(pattern, "/")
	var freshParts []string
	for _, part := range parts {
		if part == "*" {
			freshParts = append(freshParts, "shadow-subresource")
		} else {
			freshParts = append(freshParts, part)
		}
	}
	values := []string{strings.Join(freshParts, "/")}
	if strings.Contains(pattern, "*") {
		for _, replacement := range []string{"status", "log", "token", "example-widget"} {
			partsCopy := make([]string, len(parts))
			for index, part := range parts {
				if part == "*" {
					partsCopy[index] = replacement
				} else {
					partsCopy[index] = part
				}
			}
			values = appendUnique(values, strings.Join(partsCopy, "/"))
		}
	}
	return values
}

func urlCandidates(pattern string, all []Rule) []string {
	examples := collectURLExamples(all)
	if pattern == "*" || pattern == "" {
		values := appendUnique(nil, examples...)
		return appendUnique(values, freshValue("shadow-url", values))
	}

	var values []string
	for _, example := range instantiateURLPattern(pattern) {
		if matchesURLPattern(pattern, example) {
			values = appendUnique(values, example)
		}
	}
	for _, example := range examples {
		if matchesURLPattern(pattern, example) {
			values = appendUnique(values, example)
		}
	}
	return values
}

func collectURLExamples(rules []Rule) []string {
	values := []string{
		"/healthz",
		"/metrics",
		"/api/v1/example-widget",
		"/api/v1/secrets",
	}
	for _, rule := range rules {
		for _, pattern := range rule.NonResourceURLs {
			for _, example := range instantiateURLPattern(pattern) {
				values = appendUnique(values, example)
			}
		}
	}
	return values
}

func instantiateURLPattern(pattern string) []string {
	if pattern == "" {
		return nil
	}
	if pattern == "*" {
		return []string{"/analytic", "/api/v1/example-widget"}
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return []string{prefix + "/shadow-segment"}
	}
	parts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	var freshParts []string
	for _, part := range parts {
		if part == "*" {
			freshParts = append(freshParts, "shadow-segment")
		} else {
			freshParts = append(freshParts, part)
		}
	}
	return []string{"/" + strings.Join(freshParts, "/")}
}

func freshValue(base string, existing []string) string {
	value := base
	for suffix := 2; ; suffix++ {
		found := false
		for _, candidate := range existing {
			if candidate == value {
				found = true
				break
			}
		}
		if !found {
			return value
		}
		value = base + "-" + strconv.Itoa(suffix)
	}
}
