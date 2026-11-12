package netsec

import "strings"

// canonicalByAlias maps a long option name or a rsync short-option letter to
// all option families it selects. A few letters select more than one family
// (-D means devices and specials; -P means partial and progress).
var canonicalByAlias = map[string][]string{}

var shortOptionsWithArgument = map[rune]struct{}{
	'M': {},
	'B': {},
	'T': {},
	'e': {},
	'f': {},
}

func init() {
	registerOption("verbose", "v")
	registerOption("quiet", "q")
	registerOption("checksum", "c")
	registerOption("archive", "a")
	registerOption("recursive", "r")
	registerOption("relative", "R")
	registerOption("backup", "b")
	registerOption("update", "u")
	registerOption("dirs", "d")
	registerOption("links", "l")
	registerOption("perms", "p")
	registerOption("group", "g")
	registerOption("owner", "o")
	registerOption("times", "t")
	registerOption("devices", "D")
	registerOption("specials", "D")
	registerOption("sparse", "S")
	registerOption("dry-run", "n")
	registerOption("whole-file", "W")
	registerOption("one-file-system", "x")
	registerOption("acls", "A")
	registerOption("xattrs", "X")
	registerOption("hard-links", "H")
	registerOption("ignore-times", "I")
	registerOption("copy-links", "L")
	registerOption("copy-dirlinks", "k")
	registerOption("keep-dirlinks", "K")
	registerOption("omit-dir-times", "O")
	registerOption("omit-link-times", "J")
	registerOption("compress", "z")
	registerOption("rsh", "e")
	registerOption("human-readable", "h")
	registerOption("itemize-changes", "i")
	registerOption("protect-args", "s")
	registerOption("filter", "f", "F")
	registerOption("cvs-exclude", "C")
	registerOption("partial", "P")
	registerOption("progress", "P")
	registerOption("temp-dir", "T")
	registerOption("prune-empty-dirs", "m")
	registerOption("remote-option", "M")
	registerOption("block-size", "B")
	registerOption("ipv4", "4")
	registerOption("ipv6", "6")
	registerOption("8-bit-output", "8")
}

func registerOption(canonical string, aliases ...string) {
	all := append([]string{canonical}, aliases...)
	for _, alias := range all {
		canonicalByAlias[alias] = append(canonicalByAlias[alias], canonical)
	}
}

// refusedOption reports the first requested option that matches a refused
// option. Refused long options also cover their short spelling and --no-
// spelling; refusing delete covers the delete-* family as rsync does.
func refusedOption(refused []string, requested []string) (string, bool) {
	refusedNames := make(map[string]struct{})
	for _, raw := range splitConfigList(refused) {
		for _, name := range normalizedOptionNames(raw, false) {
			refusedNames[name] = struct{}{}
		}
	}
	if len(refusedNames) == 0 {
		return "", false
	}

	for _, raw := range splitOptionTokens(requested) {
		for _, name := range normalizedOptionNames(raw, true) {
			for refusedName := range refusedNames {
				if optionNamesMatch(refusedName, name) {
					return raw, true
				}
			}
		}
	}
	return "", false
}

func optionNamesMatch(refused, requested string) bool {
	if refused == requested {
		return true
	}
	// rsync treats "delete" as also refusing delete-before, delete-after,
	// delete-during, delete-delay, delete-missing-args, and delete-excluded.
	if refused == "delete" && strings.HasPrefix(requested, "delete-") {
		return true
	}
	return false
}

func normalizedOptionNames(raw string, request bool) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" || raw == "--" {
		return nil
	}

	if strings.HasPrefix(raw, "--") {
		name := raw[2:]
		if before, _, found := strings.Cut(name, "="); found {
			name = before
		}
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "no-") {
			base := name[3:]
			if len(base) == 1 {
				name = base
			} else {
				name = strings.ToLower(base)
			}
		} else {
			name = lower
		}
		return withCanonicalNames(name)
	}

	if strings.HasPrefix(raw, "-") {
		letters := raw[1:]
		var names []string
		for _, letter := range letters {
			if letter == '=' || letter == '-' {
				break
			}
			names = append(names, withCanonicalNames(string(letter))...)
			if _, takesArgument := shortOptionsWithArgument[letter]; takesArgument {
				break
			}
		}
		return names
	}

	name := raw
	if before, _, found := strings.Cut(name, "="); found {
		name = before
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "no-") {
		base := name[3:]
		if len(base) == 1 {
			name = base
		} else {
			name = strings.ToLower(base)
		}
	} else if request {
		name = lower
	}

	names := withCanonicalNames(name)
	if request && len(name) == 1 {
		names = append(names, withCanonicalNames(strings.ToLower(name))...)
	}
	return deduplicateStrings(names)
}

func withCanonicalNames(name string) []string {
	names := []string{name}
	if canonical, ok := canonicalByAlias[name]; ok {
		names = append(names, canonical...)
	}
	return deduplicateStrings(names)
}

func deduplicateStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func splitOptionTokens(values []string) []string {
	var tokens []string
	for _, value := range values {
		tokens = append(tokens, strings.Fields(value)...)
	}
	return tokens
}
