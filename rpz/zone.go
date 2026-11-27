package rpz

import (
	"errors"
	"net/netip"
	"strings"
)

// Rule is one compiled RPZ policy rule.
type Rule struct {
	// Zone is the policy zone that contributed the rule.
	Zone string
	// Trigger identifies which trigger kind matched.
	Trigger TriggerKind
	// Owner is the fully qualified RPZ owner name, including the zone apex.
	Owner string
	// Action is the policy decision applied when the rule matches.
	Action Action
	// Records holds local-data RRs for ActionLocalData (and any extra RRs).
	Records []Record
	// Prefix is populated for the three IP-based triggers.
	Prefix netip.Prefix
	// Wildcard marks a recursive wildcard QNAME/NSDNAME owner.
	Wildcard bool
	// Labels is the split match name: wildcard owners exclude the leading "*".
	Labels []string
}

// Zone is one compiled, immutable policy zone. Zones are evaluated in the
// order given to the Engine, with the first zone deciding every query.
type Zone struct {
	name string

	clientIP *bitTrie
	nsIP     *bitTrie
	answerIP *bitTrie

	// Exact name tables keyed by fully qualified lower-case name.
	qnameExact   map[string]*Rule
	nsdnameExact map[string]*Rule
	qnameWild    map[string]*Rule // keyed by wildcard parent name
	nsdnameWild  map[string]*Rule
}

// NewZone creates an empty policy zone using the standard RPZ suffix labels.
func NewZone(name string) *Zone {
	return &Zone{
		name:         name,
		clientIP:     newBitTrie(),
		nsIP:         newBitTrie(),
		answerIP:     newBitTrie(),
		qnameExact:   map[string]*Rule{},
		nsdnameExact: map[string]*Rule{},
		qnameWild:    map[string]*Rule{},
		nsdnameWild:  map[string]*Rule{},
	}
}

// Name returns the policy zone apex name.
func (z *Zone) Name() string { return z.name }

// ErrUnknownTrigger is returned when an owner name does not carry a
// recognized RPZ suffix label.
var ErrUnknownTrigger = errors.New("rpz: owner has no recognized trigger suffix")

// AddRule compiles owner (a fully qualified RPZ owner name) and records into
// the zone. Multiple RRs sharing one owner are merged onto the same rule;
// special CNAME actions take precedence over plain local data.
func (z *Zone) AddRule(owner string, records ...Record) error {
	owner = strings.TrimSpace(owner)
	labels := SplitLabels(owner)
	if len(labels) == 0 {
		return errors.New("rpz: empty owner name")
	}

	kind := TriggerQNAME
	matchLabels := labels
	var prefix netip.Prefix
	apex := SplitLabels(z.name)

	// Locate the trigger suffix label. It is either the final label (bare RPZ
	// owner) or immediately precedes the zone apex (fully qualified owner).
	suffixAt := -1
	for i, label := range labels {
		if isTriggerSuffix(label) {
			suffixAt = i
			break
		}
	}

	switch {
	case suffixAt >= 0 && labels[suffixAt] == "rpz-client-ip":
		kind = TriggerClientIP
		p, err := DecodeIPOwner(labels[:suffixAt])
		if err != nil {
			return err
		}
		prefix = p
	case suffixAt >= 0 && labels[suffixAt] == "rpz-nsip":
		kind = TriggerNSIP
		p, err := DecodeIPOwner(labels[:suffixAt])
		if err != nil {
			return err
		}
		prefix = p
	case suffixAt >= 0 && labels[suffixAt] == "rpz-ip":
		kind = TriggerIP
		p, err := DecodeIPOwner(labels[:suffixAt])
		if err != nil {
			return err
		}
		prefix = p
	case suffixAt >= 0 && labels[suffixAt] == "rpz-nsdname":
		kind = TriggerNSDNAME
		matchLabels = stripApex(labels[:suffixAt], apex)
	case suffixAt >= 0:
		return ErrUnknownTrigger
	default:
		// QNAME owners sit directly under the zone apex.
		matchLabels = stripApex(labels, apex)
	}

	wildcard := IsWildcard(matchLabels)
	nameLabels := matchLabels
	if wildcard {
		nameLabels = matchLabels[1:]
	}

	action, localRecords := classifyRecords(records)

	rule := &Rule{
		Zone:     z.name,
		Trigger:  kind,
		Owner:    JoinLabels(labels),
		Action:   action,
		Records:  localRecords,
		Prefix:   prefix,
		Wildcard: wildcard,
		Labels:   nameLabels,
	}

	switch kind {
	case TriggerClientIP:
		z.clientIP.Insert(prefix, rule)
	case TriggerNSIP:
		z.nsIP.Insert(prefix, rule)
	case TriggerIP:
		z.answerIP.Insert(prefix, rule)
	case TriggerNSDNAME:
		z.storeName(rule, z.nsdnameExact, z.nsdnameWild)
	default:
		z.storeName(rule, z.qnameExact, z.qnameWild)
	}
	return nil
}

// storeName inserts or merges a name-keyed rule, routing wildcard owners into
// the wildcard table keyed by their (wildcard-less) parent name.
func (z *Zone) storeName(rule *Rule, exact, wild map[string]*Rule) {
	key := JoinLabels(rule.Labels)
	table := exact
	if rule.Wildcard {
		table = wild
	}
	if existing := table[key]; existing != nil {
		merged := mergeRule(existing, rule)
		table[key] = merged
		return
	}
	table[key] = rule
}

// lookupName resolves an exact name plus, failing that, the most specific
// wildcard parent. Exact names always beat wildcards even when the wildcard
// owner would otherwise be equally specific.
func lookupName(labels []string, exact, wild map[string]*Rule) *Rule {
	if rule := exact[JoinLabels(labels)]; rule != nil {
		return rule
	}
	var best *Rule
	// A wildcard "*.parent" matches strict subdomains of parent only, so the
	// candidate parent must always have fewer labels than the query name.
	for cut := 1; cut < len(labels); cut++ {
		parent := labels[cut:]
		if rule := wild[JoinLabels(parent)]; rule != nil {
			if best == nil || len(rule.Labels) > len(best.Labels) {
				best = rule
			}
		}
	}
	return best
}

// lookupNameInZone resolves a query name against a name table, trying the
// full name first and then the name relative to the zone apex. Wildcard
// matching is delegated to lookupName for each candidate.
func (z *Zone) lookupNameInZone(name string, exact, wild map[string]*Rule) *Rule {
	labels := SplitLabels(name)
	if rule := lookupName(labels, exact, wild); rule != nil {
		return rule
	}
	apex := SplitLabels(z.name)
	if len(labels) > len(apex) && hasSuffixLabels(labels, apex) {
		relative := labels[:len(labels)-len(apex)]
		if rule := lookupName(relative, exact, wild); rule != nil {
			return rule
		}
	}
	return nil
}

func hasSuffixLabels(labels, suffix []string) bool {
	if len(labels) < len(suffix) {
		return false
	}
	off := len(labels) - len(suffix)
	for i, label := range suffix {
		if labels[off+i] != label {
			return false
		}
	}
	return true
}

// stripApex removes the zone apex suffix when present, yielding the trigger's
// match name. Owners outside the apex are accepted unchanged so zones can be
// populated programmatically with bare RPZ owner names.
func stripApex(labels, apex []string) []string {
	if len(labels) > len(apex) && hasSuffixLabels(labels, apex) {
		return labels[:len(labels)-len(apex)]
	}
	return labels
}

func isTriggerSuffix(label string) bool {
	switch label {
	case "rpz-client-ip", "rpz-nsip", "rpz-ip", "rpz-nsdname":
		return true
	default:
		return false
	}
}

// classifyRecords turns parsed RRs into an action and local-data records. A
// special CNAME target selects one of the five fixed actions; otherwise the
// rule is local data.
func classifyRecords(records []Record) (Action, []Record) {
	action := ActionLocalData
	var local []Record
	for _, record := range records {
		if record.Type == "CNAME" {
			switch record.Value {
			case ".":
				action = ActionNXDOMAIN
				continue
			case "*.":
				action = ActionNODATA
				continue
			case "rpz-passthru.":
				action = ActionPASSTHRU
				continue
			case "rpz-drop.":
				action = ActionDROP
				continue
			case "rpz-tcp-only.":
				action = ActionTCPOnly
				continue
			}
		}
		local = append(local, record)
	}
	// Special actions dominate plain data when an owner mixes both forms.
	if action != ActionLocalData {
		return action, local
	}
	return action, local
}

// mergeRule combines two rules sharing one owner. A fixed action found on
// either side wins; local records are concatenated without duplication.
func mergeRule(existing, incoming *Rule) *Rule {
	if existing == nil {
		return incoming
	}
	rule := *incoming
	if incoming.Action == ActionLocalData && existing.Action != ActionLocalData {
		rule.Action = existing.Action
	}
	seen := map[Record]bool{}
	for _, record := range existing.Records {
		if !seen[record] {
			seen[record] = true
			rule.Records = append(rule.Records, record)
		}
	}
	return &rule
}
