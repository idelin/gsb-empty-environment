package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

var (
	// ErrInvalidPattern is returned when a binding key is not a valid topic
	// pattern.
	ErrInvalidPattern = errors.New("invalid binding pattern")

	// ErrInvalidRoutingKey is returned when a routing key contains a wildcard
	// or has an invalid segment layout.
	ErrInvalidRoutingKey = errors.New("invalid routing key")
)

type tokenKind uint8

const (
	tokenLiteral tokenKind = iota
	tokenSingle
	tokenMulti
)

type topicToken struct {
	kind  tokenKind
	value string
}

// Pattern is a compiled topic binding key. A binding key is split on dots;
// "*" matches exactly one segment and "#" matches zero or more segments.
// Wildcards are only accepted as complete segments.
type Pattern struct {
	raw    string
	tokens []topicToken
}

// ParsePattern parses a binding key into a Pattern.
func ParsePattern(bindingKey string) (Pattern, error) {
	segments, err := splitSegments(bindingKey, true)
	if err != nil {
		return Pattern{}, fmt.Errorf("%w %q: %v", ErrInvalidPattern, bindingKey, err)
	}

	tokens := make([]topicToken, len(segments))
	for i, segment := range segments {
		switch segment {
		case "*":
			tokens[i] = topicToken{kind: tokenSingle}
		case "#":
			tokens[i] = topicToken{kind: tokenMulti}
		default:
			tokens[i] = topicToken{kind: tokenLiteral, value: segment}
		}
	}
	return Pattern{raw: bindingKey, tokens: tokens}, nil
}

// ParseBindingKey is an alias for ParsePattern.
func ParseBindingKey(bindingKey string) (Pattern, error) {
	return ParsePattern(bindingKey)
}

// ParseTopic splits and validates a concrete routing key. Routing keys are
// never allowed to contain wildcard segments.
func ParseTopic(routingKey string) ([]string, error) {
	segments, err := splitSegments(routingKey, false)
	if err != nil {
		return nil, fmt.Errorf("%w %q: %v", ErrInvalidRoutingKey, routingKey, err)
	}
	return segments, nil
}

// ParseRoutingKey is an alias for ParseTopic.
func ParseRoutingKey(routingKey string) ([]string, error) {
	return ParseTopic(routingKey)
}

func splitSegments(key string, allowWildcards bool) ([]string, error) {
	if key == "" {
		return nil, nil
	}
	if strings.IndexByte(key, 0) >= 0 {
		return nil, errors.New("NUL byte is not allowed")
	}

	segments := strings.Split(key, ".")
	for _, segment := range segments {
		if segment == "" {
			return nil, errors.New("empty topic segment")
		}
		if !allowWildcards && strings.ContainsAny(segment, "*#") {
			return nil, errors.New("wildcards are not allowed in routing keys")
		}
		if allowWildcards && segment != "*" && segment != "#" && strings.ContainsAny(segment, "*#") {
			return nil, errors.New("wildcards must occupy a complete segment")
		}
	}
	return segments, nil
}

// String returns the binding key from which the pattern was parsed.
func (p Pattern) String() string {
	return p.raw
}

// Segments returns a copy of the pattern's segments.
func (p Pattern) Segments() []string {
	segments := make([]string, len(p.tokens))
	for i, token := range p.tokens {
		switch token.kind {
		case tokenSingle:
			segments[i] = "*"
		case tokenMulti:
			segments[i] = "#"
		default:
			segments[i] = token.value
		}
	}
	return segments
}

// Matches reports whether the pattern matches a concrete routing key. Invalid
// routing keys never match.
func (p Pattern) Matches(routingKey string) bool {
	segments, err := ParseTopic(routingKey)
	if err != nil {
		return false
	}
	return p.matchesSegments(segments)
}

// Match is an alias for Matches.
func (p Pattern) Match(routingKey string) bool {
	return p.Matches(routingKey)
}

func (p Pattern) matchesSegments(route []string) bool {
	type position struct {
		pattern int
		route   int
	}

	memo := make(map[position]bool)
	seen := make(map[position]bool)

	var match func(patternIndex, routeIndex int) bool
	match = func(patternIndex, routeIndex int) bool {
		state := position{pattern: patternIndex, route: routeIndex}
		if seen[state] {
			return memo[state]
		}
		seen[state] = true

		result := false
		switch {
		case patternIndex == len(p.tokens):
			result = routeIndex == len(route)
		case p.tokens[patternIndex].kind == tokenMulti:
			result = match(patternIndex+1, routeIndex) ||
				(routeIndex < len(route) && match(patternIndex, routeIndex+1))
		case routeIndex == len(route):
			result = false
		case p.tokens[patternIndex].kind == tokenSingle:
			result = match(patternIndex+1, routeIndex+1)
		case p.tokens[patternIndex].value == route[routeIndex]:
			result = match(patternIndex+1, routeIndex+1)
		}

		memo[state] = result
		return result
	}

	return match(0, 0)
}

// Covers reports whether every topic matched by requested is also matched by
// p. This is a language-containment check, not a one-off wildcard match: in
// particular, "#" is never covered by a literal or "*" pattern.
func (p Pattern) Covers(requested Pattern) bool {
	return coversTokens(p.tokens, requested.tokens)
}

type coverPosition struct {
	allowed   string
	requested string
}

func coversTokens(allowed, requested []topicToken) bool {
	allowedNFA := topicNFA{tokens: allowed, states: len(allowed) + 1}
	requestedNFA := topicNFA{tokens: requested, states: len(requested) + 1}

	alphabet := transitionAlphabet(allowed, requested)
	start := coverPosition{
		allowed:   allowedNFA.key(allowedNFA.closure(allowedNFA.start())),
		requested: requestedNFA.key(requestedNFA.closure(requestedNFA.start())),
	}

	type productState struct {
		allowed   stateSet
		requested stateSet
	}

	startAllowed := allowedNFA.closure(allowedNFA.start())
	startRequested := requestedNFA.closure(requestedNFA.start())
	queue := []productState{{allowed: startAllowed, requested: startRequested}}
	visited := map[coverPosition]struct{}{start: {}}

	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]

		if requestedNFA.accepts(current.requested) && !allowedNFA.accepts(current.allowed) {
			return false
		}

		for _, symbol := range alphabet {
			next := productState{
				allowed:   allowedNFA.step(current.allowed, symbol),
				requested: requestedNFA.step(current.requested, symbol),
			}
			key := coverPosition{
				allowed:   allowedNFA.key(next.allowed),
				requested: requestedNFA.key(next.requested),
			}
			if _, ok := visited[key]; ok {
				continue
			}
			visited[key] = struct{}{}
			queue = append(queue, next)
		}
	}
	return true
}

type stateSet []uint64

type topicNFA struct {
	tokens []topicToken
	states int
}

func (n topicNFA) start() stateSet {
	set := makeStateSet(n.states)
	set.add(0)
	return set
}

func (n topicNFA) acceptState() int {
	return n.states - 1
}

func (n topicNFA) accepts(set stateSet) bool {
	return set.has(n.acceptState())
}

func (n topicNFA) closure(set stateSet) stateSet {
	closed := set.clone()
	queue := closed.states()
	for len(queue) != 0 {
		state := queue[0]
		queue = queue[1:]
		if state >= len(n.tokens) || n.tokens[state].kind != tokenMulti {
			continue
		}
		next := state + 1
		if !closed.has(next) {
			closed.add(next)
			queue = append(queue, next)
		}
	}
	return closed
}

func (n topicNFA) step(set stateSet, symbol string) stateSet {
	next := makeStateSet(n.states)
	for _, state := range set.states() {
		if state >= len(n.tokens) {
			continue
		}
		token := n.tokens[state]
		switch token.kind {
		case tokenLiteral:
			if token.value == symbol {
				next.add(state + 1)
			}
		case tokenSingle:
			next.add(state + 1)
		case tokenMulti:
			next.add(state)
		}
	}
	return n.closure(next)
}

func (n topicNFA) key(set stateSet) string {
	buffer := make([]byte, len(set)*8)
	for i, word := range set {
		binary.LittleEndian.PutUint64(buffer[i*8:], word)
	}
	return string(buffer)
}

func transitionAlphabet(patterns ...[]topicToken) []string {
	seen := make(map[string]struct{})
	alphabet := make([]string, 0)
	for _, pattern := range patterns {
		for _, token := range pattern {
			if token.kind != tokenLiteral {
				continue
			}
			if _, ok := seen[token.value]; ok {
				continue
			}
			seen[token.value] = struct{}{}
			alphabet = append(alphabet, token.value)
		}
	}
	alphabet = append(alphabet, "\x00")
	return alphabet
}

func makeStateSet(states int) stateSet {
	return make(stateSet, (states+63)/64)
}

func (s stateSet) clone() stateSet {
	cloned := make(stateSet, len(s))
	copy(cloned, s)
	return cloned
}

func (s stateSet) add(state int) {
	s[state/64] |= uint64(1) << uint(state%64)
}

func (s stateSet) has(state int) bool {
	return s[state/64]&(uint64(1)<<uint(state%64)) != 0
}

func (s stateSet) states() []int {
	states := make([]int, 0, len(s)*4)
	for wordIndex, word := range s {
		for word != 0 {
			offset := bits.TrailingZeros64(word)
			states = append(states, wordIndex*64+offset)
			word &^= uint64(1) << uint(offset)
		}
	}
	return states
}

// CoveredBy reports whether p is a subset of the allowed pattern.
func (p Pattern) CoveredBy(allowed Pattern) bool {
	return allowed.Covers(p)
}

// MatchTopic reports whether a binding key pattern matches a concrete routing
// key. Malformed input fails closed.
func MatchTopic(bindingKey, routingKey string) bool {
	pattern, err := ParsePattern(bindingKey)
	if err != nil {
		return false
	}
	return pattern.Matches(routingKey)
}

// MatchBinding is an alias for MatchTopic.
func MatchBinding(bindingKey, routingKey string) bool {
	return MatchTopic(bindingKey, routingKey)
}
