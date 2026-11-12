package netsec

import "strings"

// normalizeName lower-cases a DNS name and gives it a single trailing dot,
// so that "host.local", "host.local." and "HOST.LOCAL" compare equal.
func normalizeName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}

// labels splits a normalized name into its labels, dropping the empty label
// produced by the trailing dot.
func labels(name string) []string {
	n := strings.TrimSuffix(normalizeName(name), ".")
	if n == "" {
		return nil
	}
	return strings.Split(n, ".")
}

// isLocalName reports whether name lives in the .local special-use domain
// (RFC 6762). Only names ending in ".local" or strictly below it qualify;
// a look-alike such as "local.evil." does not.
func isLocalName(name string) bool {
	ls := labels(name)
	if len(ls) < 2 {
		return false
	}
	return ls[len(ls)-1] == "local"
}

// blockedMulticastNames are names enterprise policy forbids resolving via
// multicast at all. WPAD lets an attacker hijack the browser proxy
// configuration; ISATAP lets an attacker inject an IPv6 transition router.
// Both are classic responder-poisoning targets.
var blockedMulticastNames = map[string]bool{
	"wpad":   true,
	"isatap": true,
}

// isBlockedName reports whether the leftmost label of name is a forbidden
// multicast name. Checking the first label covers both the bare name
// ("wpad.local.") and the per-domain form ("wpad.corp.example.").
func isBlockedName(name string) bool {
	ls := labels(name)
	if len(ls) == 0 {
		return false
	}
	return blockedMulticastNames[ls[0]]
}

// allNames collects the normalized owner name and every RDATA-embedded name
// from a message, across all three record sections and the question
// section. It is used to enforce the wpad/isatap ban packet-wide.
func allNames(msg *Message) []string {
	var out []string
	add := func(n string) {
		if n = normalizeName(n); n != "" {
			out = append(out, n)
		}
	}
	for i := range msg.Questions {
		add(msg.Questions[i].Name)
	}
	for _, section := range [][]Record{msg.Answers, msg.Authority, msg.Additional} {
		for i := range section {
			rec := &section[i]
			add(rec.Name)
			if rec.Data != nil {
				for _, target := range rec.Data.targets() {
					add(target)
				}
			}
		}
	}
	return out
}
