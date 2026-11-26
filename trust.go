package krb5policy

import (
	"fmt"
	"sort"
	"strings"
)

// TrustEdge declares a directed cross-realm trust.
//
// From -> To means that a ticket may transit From and then be honored by To:
// the From realm can issue the inter-realm ticket/key material accepted by To.
// Bidirectional adds the reverse direction.  The direction is intentionally
// explicit because "A trusts B" is ambiguous in prose but not in a policy
// verifier.
type TrustEdge struct {
	From          string
	To            string
	Bidirectional bool
}

// CapathPolicy controls how an explicit [capaths] rule is enforced.
type CapathPolicy int

const (
	// CapathStrictSet requires every configured intermediate realm to appear
	// exactly once and prohibits unlisted realms.  Order is flexible except for
	// the optional first-hop check.  This is the default and the fail-closed
	// setting.
	CapathStrictSet CapathPolicy = iota

	// CapathAllowedSubset treats configured realms as an allow-list.  Every
	// declared intermediate must be listed, but not every listed realm must be
	// visited.  A direct shortcut is still rejected when the list is non-empty.
	CapathAllowedSubset

	// CapathExactSequence requires byte-for-byte (case-insensitive realm)
	// sequence equality with the configured intermediate list.
	CapathExactSequence
)

// CapathRule is one explicit client-realm -> server-realm policy entry.
// Intermediates excludes both endpoints.
type CapathRule struct {
	ClientRealm   string
	ServerRealm   string
	Intermediates []string
}

// TrustGraphConfig is a complete, declarative host policy.
type TrustGraphConfig struct {
	// Realms optionally declares realm nodes that have no trust edges.  This
	// is useful for auditing isolated realms and for adversarial search models.
	Realms  []string
	Edges   []TrustEdge
	Capaths []CapathRule
	Policy  CapathPolicy
	// RequireFirstCapathHop additionally requires the first declared
	// intermediate to be the path's first intermediate.  It is off by default
	// because KDC-side RFC 4120 verification normally cares that a realm is on
	// the allowed route, not about the client's local hop preference.
	RequireFirstCapathHop bool
	// AllowDirectWithCapath permits a direct client->server ticket even when a
	// capath lists intermediate realms.  It is false by default so that an
	// explicit route cannot be bypassed by a shortcut.
	AllowDirectWithCapath bool
	MaxPathLength         int
	MaxRealms             int
}

// PathValidationOptions overrides graph-level capath behavior for one call.
type PathValidationOptions struct {
	Policy                *CapathPolicy
	RequireFirstCapathHop *bool
	AllowDirectWithCapath *bool
}

// PathHop is one directed trust traversal.
type PathHop struct {
	From string
	To   string
}

// ValidatedPath is the result of an authorized path check.
type ValidatedPath struct {
	Client   string
	Server   string
	Realms   []string
	FullPath []string
	Hops     []PathHop
	Capath   *CapathRule
}

type realmPair struct {
	client string
	server string
}

type capathEntry struct {
	rule    CapathRule
	hopKeys []string
}

// TrustGraph is an immutable, validated cross-realm policy graph.
type TrustGraph struct {
	display     map[string]string
	adj         map[string]map[string]struct{}
	capaths     map[realmPair]capathEntry
	policy      CapathPolicy
	reqFirst    bool
	allowDirect bool
	maxPath     int
	maxRealms   int
}

// NewTrustGraph validates and compiles a trust graph.
func NewTrustGraph(cfg TrustGraphConfig) (*TrustGraph, error) {
	policy := cfg.Policy
	if policy < CapathStrictSet || policy > CapathExactSequence {
		return nil, fmt.Errorf("%w: unknown capath policy %d", ErrInvalidConfig, policy)
	}
	maxPath := cfg.MaxPathLength
	if maxPath <= 0 {
		maxPath = 16
	}
	maxRealms := cfg.MaxRealms
	if maxRealms <= 0 {
		maxRealms = 1024
	}
	g := &TrustGraph{
		display:     make(map[string]string),
		adj:         make(map[string]map[string]struct{}),
		capaths:     make(map[realmPair]capathEntry),
		policy:      policy,
		reqFirst:    cfg.RequireFirstCapathHop,
		allowDirect: cfg.AllowDirectWithCapath,
		maxPath:     maxPath,
		maxRealms:   maxRealms,
	}
	if len(cfg.Edges) > maxRealms*maxRealms {
		return nil, fmt.Errorf("%w: too many trust edges", ErrInvalidConfig)
	}
	for i, realm := range cfg.Realms {
		if _, err := g.realm(realm); err != nil {
			return nil, fmt.Errorf("realm %d: %w", i, err)
		}
	}
	for i, edge := range cfg.Edges {
		from, err := g.realm(edge.From)
		if err != nil {
			return nil, fmt.Errorf("edge %d from: %w", i, err)
		}
		to, err := g.realm(edge.To)
		if err != nil {
			return nil, fmt.Errorf("edge %d to: %w", i, err)
		}
		if from == to {
			return nil, fmt.Errorf("%w: self trust edge %q", ErrInvalidConfig, edge.From)
		}
		g.addEdge(from, to)
		if edge.Bidirectional {
			g.addEdge(to, from)
		}
	}
	for i, rule := range cfg.Capaths {
		clientKey, err := g.realm(rule.ClientRealm)
		if err != nil {
			return nil, fmt.Errorf("capath %d client: %w", i, err)
		}
		serverKey, err := g.realm(rule.ServerRealm)
		if err != nil {
			return nil, fmt.Errorf("capath %d server: %w", i, err)
		}
		if clientKey == serverKey {
			return nil, fmt.Errorf("%w: capath has identical endpoints %q", ErrInvalidConfig, rule.ClientRealm)
		}
		pair := realmPair{client: clientKey, server: serverKey}
		if _, exists := g.capaths[pair]; exists {
			return nil, fmt.Errorf("%w: duplicate capath %s -> %s", ErrInvalidConfig, rule.ClientRealm, rule.ServerRealm)
		}
		if len(rule.Intermediates) > maxPath {
			return nil, fmt.Errorf("%w: capath has %d intermediates > %d", ErrInvalidConfig, len(rule.Intermediates), maxPath)
		}
		seen := make(map[string]struct{}, len(rule.Intermediates))
		hopKeys := make([]string, 0, len(rule.Intermediates))
		displayHops := make([]string, 0, len(rule.Intermediates))
		for j, hop := range rule.Intermediates {
			key, err := g.realm(hop)
			if err != nil {
				return nil, fmt.Errorf("capath %d intermediate %d: %w", i, j, err)
			}
			if key == clientKey || key == serverKey {
				return nil, fmt.Errorf("%w: capath intermediate must not be an endpoint: %q", ErrInvalidConfig, hop)
			}
			if _, dup := seen[key]; dup {
				return nil, fmt.Errorf("%w: duplicate capath intermediate %q", ErrInvalidConfig, hop)
			}
			seen[key] = struct{}{}
			hopKeys = append(hopKeys, key)
			displayHops = append(displayHops, g.display[key])
		}
		g.capaths[pair] = capathEntry{
			rule: CapathRule{
				ClientRealm:   g.display[clientKey],
				ServerRealm:   g.display[serverKey],
				Intermediates: displayHops,
			},
			hopKeys: hopKeys,
		}
	}
	return g, nil
}

func (g *TrustGraph) realm(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: empty realm", ErrInvalidRealm)
	}
	if err := validateRealm(raw); err != nil {
		return "", err
	}
	key := canonicalRealm(raw)
	if _, ok := g.display[key]; !ok {
		if len(g.display) >= g.maxRealms {
			return "", fmt.Errorf("%w: too many realms", ErrInvalidConfig)
		}
		g.display[key] = raw
	}
	return key, nil
}

func (g *TrustGraph) addEdge(from, to string) {
	if g.adj[from] == nil {
		g.adj[from] = make(map[string]struct{})
	}
	g.adj[from][to] = struct{}{}
}

// Realms returns the configured realm names in stable order.
func (g *TrustGraph) Realms() []string {
	out := make([]string, 0, len(g.display))
	for _, name := range g.display {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// HasDirectTrust reports whether one directed hop from -> to is configured.
func (g *TrustGraph) HasDirectTrust(from, to string) bool {
	a, ok := g.key(from)
	if !ok {
		return false
	}
	b, ok := g.key(to)
	if !ok {
		return false
	}
	_, ok = g.adj[a][b]
	return ok
}

func (g *TrustGraph) key(raw string) (string, bool) {
	key := canonicalRealm(strings.TrimSpace(raw))
	_, ok := g.display[key]
	return key, ok
}

func (g *TrustGraph) lookup(raw string) (string, string, bool) {
	key := canonicalRealm(strings.TrimSpace(raw))
	display, ok := g.display[key]
	return key, display, ok
}

// Successors returns the display names directly reachable from realm.
func (g *TrustGraph) Successors(realm string) []string {
	key, ok := g.key(realm)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(g.adj[key]))
	for next := range g.adj[key] {
		out = append(out, g.display[next])
	}
	sort.Strings(out)
	return out
}

// Capath returns a copy of the explicit rule for client -> server.
func (g *TrustGraph) Capath(client, server string) (CapathRule, bool) {
	c, ok := g.key(client)
	if !ok {
		return CapathRule{}, false
	}
	s, ok := g.key(server)
	if !ok {
		return CapathRule{}, false
	}
	e, ok := g.capaths[realmPair{client: c, server: s}]
	if !ok {
		return CapathRule{}, false
	}
	rule := e.rule
	rule.Intermediates = append([]string(nil), rule.Intermediates...)
	return rule, true
}

// ValidatePath checks a transited intermediate list.
//
// Endpoint realms may be redundantly present as the first or last element of
// transited; they are stripped exactly once before validation.  Interior
// occurrences are treated as cycles and rejected.
func (g *TrustGraph) ValidatePath(client, server string, transited []string) (*ValidatedPath, error) {
	return g.ValidatePathWithOptions(client, server, transited, PathValidationOptions{})
}

// ValidatePathWithOptions is ValidatePath with per-call policy overrides.
func (g *TrustGraph) ValidatePathWithOptions(client, server string, transited []string, opts PathValidationOptions) (*ValidatedPath, error) {
	if len(transited) > g.maxPath+2 {
		return nil, fmt.Errorf("%w: %d input realms > %d", ErrPathLimit, len(transited), g.maxPath+2)
	}
	clientKey, clientName, ok := g.lookup(client)
	if !ok {
		return nil, fmt.Errorf("%w: unknown client realm %q", ErrPathNotAuthorized, strings.TrimSpace(client))
	}
	serverKey, serverName, ok := g.lookup(server)
	if !ok {
		return nil, fmt.Errorf("%w: unknown server realm %q", ErrPathNotAuthorized, strings.TrimSpace(server))
	}
	policy := g.policy
	if opts.Policy != nil {
		policy = *opts.Policy
	}
	if policy < CapathStrictSet || policy > CapathExactSequence {
		return nil, fmt.Errorf("%w: bad policy override %d", ErrInvalidConfig, policy)
	}
	reqFirst := g.reqFirst
	if opts.RequireFirstCapathHop != nil {
		reqFirst = *opts.RequireFirstCapathHop
	}
	allowDirect := g.allowDirect
	if opts.AllowDirectWithCapath != nil {
		allowDirect = *opts.AllowDirectWithCapath
	}

	displayRealms := make([]string, 0, len(transited))
	keys := make([]string, 0, len(transited))
	for i, raw := range transited {
		key, display, ok := g.lookup(raw)
		if !ok {
			return nil, fmt.Errorf("%w: transited realm %d %q is unknown", ErrPathNotAuthorized, i+1, strings.TrimSpace(raw))
		}
		keys = append(keys, key)
		displayRealms = append(displayRealms, display)
	}
	if len(keys) > 0 && keys[0] == clientKey {
		keys = keys[1:]
		displayRealms = displayRealms[1:]
	}
	if len(keys) > 0 && keys[len(keys)-1] == serverKey {
		keys = keys[:len(keys)-1]
		displayRealms = displayRealms[:len(displayRealms)-1]
	}
	if len(keys)+1 > g.maxPath {
		return nil, fmt.Errorf("%w: %d hops > %d", ErrPathLimit, len(keys)+1, g.maxPath)
	}
	if clientKey == serverKey {
		if len(keys) != 0 {
			return nil, fmt.Errorf("%w: non-empty path for identical realms", ErrPathNotAuthorized)
		}
		return &ValidatedPath{
			Client:   clientName,
			Server:   serverName,
			FullPath: []string{clientName},
		}, nil
	}

	fullKeys := make([]string, 0, len(keys)+2)
	fullKeys = append(fullKeys, clientKey)
	fullKeys = append(fullKeys, keys...)
	fullKeys = append(fullKeys, serverKey)
	seen := make(map[string]struct{}, len(fullKeys))
	for i, key := range fullKeys {
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: repeated realm %q at hop %d", ErrRealmLoop, g.display[key], i)
		}
		seen[key] = struct{}{}
	}

	hops := make([]PathHop, 0, len(fullKeys)-1)
	for i := 0; i+1 < len(fullKeys); i++ {
		from, to := fullKeys[i], fullKeys[i+1]
		if _, ok := g.adj[from][to]; !ok {
			return nil, fmt.Errorf("%w: %s -> %s", ErrNoTrust, g.display[from], g.display[to])
		}
		// Pairwise capaths entries prohibit turning a pair that explicitly
		// requires a multi-hop traversal into a direct shortcut.
		if entry, ok := g.capaths[realmPair{client: from, server: to}]; ok && len(entry.hopKeys) > 0 && !allowDirect {
			return nil, fmt.Errorf("%w: direct hop %s -> %s bypasses configured intermediates", ErrTransitShortcut, g.display[from], g.display[to])
		}
		hops = append(hops, PathHop{From: g.display[from], To: g.display[to]})
	}

	fullPath := make([]string, len(fullKeys))
	for i, key := range fullKeys {
		fullPath[i] = g.display[key]
	}
	var usedCapath *CapathRule
	if entry, ok := g.capaths[realmPair{client: clientKey, server: serverKey}]; ok {
		rule := entry.rule
		rule.Intermediates = append([]string(nil), rule.Intermediates...)
		usedCapath = &rule
		if err := validateCapath(policy, reqFirst, allowDirect, entry, keys, g); err != nil {
			return nil, err
		}
	}

	return &ValidatedPath{
		Client:   clientName,
		Server:   serverName,
		Realms:   displayRealms,
		FullPath: fullPath,
		Hops:     hops,
		Capath:   usedCapath,
	}, nil
}

func validateCapath(policy CapathPolicy, reqFirst, allowDirect bool, entry capathEntry, keys []string, g *TrustGraph) error {
	display := func(keys []string) []string {
		out := make([]string, len(keys))
		for i, k := range keys {
			out[i] = g.display[k]
		}
		return out
	}
	if len(entry.hopKeys) == 0 {
		if len(keys) != 0 && !allowDirect {
			return fmt.Errorf("%w: capath requires a direct traversal, got %v", ErrCapathViolation, display(keys))
		}
		return nil
	}
	if len(keys) == 0 && !allowDirect {
		return fmt.Errorf("%w: direct shortcut bypasses required capath intermediates %v", ErrTransitShortcut, display(entry.hopKeys))
	}
	if len(keys) == 0 {
		return nil
	}
	switch policy {
	case CapathExactSequence:
		if len(keys) != len(entry.hopKeys) {
			return fmt.Errorf("%w: capath sequence length %d != %d", ErrCapathViolation, len(keys), len(entry.hopKeys))
		}
		for i := range keys {
			if keys[i] != entry.hopKeys[i] {
				return fmt.Errorf("%w: capath sequence mismatch at position %d", ErrCapathViolation, i+1)
			}
		}
	case CapathAllowedSubset:
		allowed := make(map[string]struct{}, len(entry.hopKeys))
		for _, h := range entry.hopKeys {
			allowed[h] = struct{}{}
		}
		for _, k := range keys {
			if _, ok := allowed[k]; !ok {
				return fmt.Errorf("%w: realm %q is not in capath allow-list", ErrCapathViolation, g.display[k])
			}
		}
	case CapathStrictSet:
		if len(keys) != len(entry.hopKeys) {
			return fmt.Errorf("%w: capath requires %d intermediate realms, got %d", ErrCapathViolation, len(entry.hopKeys), len(keys))
		}
		expected := make(map[string]struct{}, len(entry.hopKeys))
		for _, h := range entry.hopKeys {
			expected[h] = struct{}{}
		}
		for _, k := range keys {
			if _, ok := expected[k]; !ok {
				return fmt.Errorf("%w: realm %q is not in capath", ErrCapathViolation, g.display[k])
			}
			delete(expected, k)
		}
		if len(expected) != 0 {
			return fmt.Errorf("%w: capath realms not visited: %v", ErrCapathViolation, display(keysOf(expected, g)))
		}
	default:
		return fmt.Errorf("%w: unknown policy", ErrInvalidConfig)
	}
	if reqFirst && len(keys) > 0 && keys[0] != entry.hopKeys[0] {
		return fmt.Errorf("%w: first hop %q != configured %q", ErrCapathViolation, g.display[keys[0]], g.display[entry.hopKeys[0]])
	}
	return nil
}

func keysOf(set map[string]struct{}, g *TrustGraph) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidateExpanded checks that an expanded transited field closed at the
// server realm and that every declared realm is on an authorized path.
func (g *TrustGraph) ValidateExpanded(client, server string, exp *ExpandedTransited) (*ValidatedPath, error) {
	if exp == nil {
		return nil, fmt.Errorf("%w: nil expanded transited field", ErrPathNotAuthorized)
	}
	if !exp.Complete {
		return nil, fmt.Errorf("%w: trailing empty item disabled implicit completion", ErrIncompleteTransit)
	}
	return g.ValidatePath(client, server, exp.Realms)
}
