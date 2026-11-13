package netsec

import (
	"fmt"
	"sort"
)

// AllowedLanguage describes the finite values a claim may take for collision
// auditing. Set either Values, or Prefix together with Suffixes, but not both.
// An empty string is never an allowed value.
type AllowedLanguage struct {
	Values   []string
	Prefix   string
	Suffixes []string
}

// EnumLanguage declares a finite, explicit set of allowed values.
func EnumLanguage(values ...string) AllowedLanguage {
	return AllowedLanguage{Values: append([]string(nil), values...)}
}

// FiniteLanguage is an alias for EnumLanguage.
func FiniteLanguage(values ...string) AllowedLanguage {
	return EnumLanguage(values...)
}

// PrefixSuffixLanguage declares prefix+suffix values for a finite suffix set.
func PrefixSuffixLanguage(prefix string, suffixes ...string) AllowedLanguage {
	return AllowedLanguage{Prefix: prefix, Suffixes: append([]string(nil), suffixes...)}
}

type language struct {
	words     []string
	prefix    string
	suffixes  []string
	suffixSet map[string]struct{}
	enumSet   map[string]struct{}
}

func compileLanguage(claim ClaimName, definition AllowedLanguage) (*language, error) {
	if len(definition.Values) > 0 && (definition.Prefix != "" || len(definition.Suffixes) > 0) {
		return nil, fmt.Errorf("%w: claim %q mixes enum values and prefix/suffix values", ErrInvalidConfig, claim)
	}
	if len(definition.Values) > 0 {
		words := make([]string, 0, len(definition.Values))
		for _, word := range definition.Values {
			if word == "" {
				return nil, fmt.Errorf("%w: claim %q allows the empty string", ErrInvalidConfig, claim)
			}
			words = append(words, word)
		}
		words = sortedUniqueStrings(words)
		enumSet := make(map[string]struct{}, len(words))
		for _, word := range words {
			enumSet[word] = struct{}{}
		}
		return &language{words: words, enumSet: enumSet}, nil
	}
	if len(definition.Suffixes) == 0 {
		return nil, fmt.Errorf("%w: claim %q has no allowed values", ErrInvalidConfig, claim)
	}

	suffixes := make([]string, 0, len(definition.Suffixes))
	suffixSet := make(map[string]struct{}, len(definition.Suffixes))
	for _, suffix := range definition.Suffixes {
		if definition.Prefix == "" && suffix == "" {
			return nil, fmt.Errorf("%w: claim %q allows the empty string", ErrInvalidConfig, claim)
		}
		if _, exists := suffixSet[suffix]; exists {
			continue
		}
		suffixSet[suffix] = struct{}{}
		suffixes = append(suffixes, suffix)
	}
	sort.Strings(suffixes)
	return &language{
		prefix:    definition.Prefix,
		suffixes:  suffixes,
		suffixSet: suffixSet,
	}, nil
}

func (l *language) len() int {
	if l.words != nil {
		return len(l.words)
	}
	return len(l.suffixes)
}

// wordAt returns values in byte-lexicographic order. Prefix+suffix values
// share the prefix, so sorting the suffixes is sufficient.
func (l *language) wordAt(index int) string {
	if l.words != nil {
		return l.words[index]
	}
	return l.prefix + l.suffixes[index]
}

func (l *language) contains(value string) bool {
	if l.words != nil {
		_, ok := l.enumSet[value]
		return ok
	}
	if len(value) < len(l.prefix) || value[:len(l.prefix)] != l.prefix {
		return false
	}
	_, ok := l.suffixSet[value[len(l.prefix):]]
	return ok
}

func sortedUniqueStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	if len(result) < 2 {
		return result
	}
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write]
}
