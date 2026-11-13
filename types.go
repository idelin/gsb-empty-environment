package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// KeywordSet is one plaintext document's set of keyword IDs. Repeated IDs in
// a set are ignored.
type KeywordSet = []string

// Document is a plaintext document's keyword set.
type Document = []string

// Token describes one anonymous query token and the anonymous document IDs
// that it hits. Repeated document IDs in a token are ignored.
type Token struct {
	ID        string
	Documents []string
}

// TokenHits is an alternative name for Token, useful when constructing input
// from a server-side hit list.
type TokenHits = Token

// KnownMatch is an optional known token-to-keyword correspondence.
type KnownMatch struct {
	TokenID   string
	KeywordID string
}

// KnownCorrespondence is an alternative name for KnownMatch.
type KnownCorrespondence = KnownMatch

// Input is a complete leakage-analysis problem.
type Input struct {
	// Keywords contains at most 18 unique keyword IDs.
	Keywords []string

	// Documents contains the plaintext keyword sets. Each slice element is a
	// separate document, including elements with identical contents.
	Documents []Document

	// Tokens contains exactly one anonymous token per keyword.
	Tokens []Token

	// Known optionally supplies known token-to-keyword correspondences. The
	// supplied correspondences need not be complete, but must be injective.
	Known []KnownMatch

	// MaxMappings limits retained complete mappings. Zero means 20. Values
	// above 20 are capped at 20.
	MaxMappings int

	// SearchBudget is the maximum number of search branches to examine. Zero
	// means DefaultSearchBudget. Exhausting the budget never implies that a
	// correspondence is fixed.
	SearchBudget uint64
}

// Options controls search effort and retained output.
type Options struct {
	// MaxMappings limits retained complete mappings. Zero means 20. Values
	// above 20 are capped at 20.
	MaxMappings int

	// SearchBudget is the maximum number of search branches. Zero means
	// DefaultSearchBudget.
	SearchBudget uint64

	// NodeBudget is an alias for SearchBudget. SearchBudget takes precedence
	// when both are set.
	NodeBudget uint64

	// Limit is an alias for MaxMappings. MaxMappings takes precedence when
	// both are set.
	Limit int
}

// DefaultSearchBudget is deliberately finite. A finite budget is necessary
// because a corpus can contain up to 18 mutually symmetric keywords, whose
// feasible mappings are factorial in number.
const DefaultSearchBudget uint64 = 1_000_000

// MaxRetainedMappings is the hard output limit required by the analysis.
const MaxRetainedMappings = 20

// PairKey is an unordered pair of IDs, stored in byte-sorted order.
type PairKey [2]string

// A returns the byte-lexicographically smaller ID in the pair.
func (p PairKey) A() string { return p[0] }

// B returns the byte-lexicographically larger ID in the pair.
func (p PairKey) B() string { return p[1] }

// Statistics is the first- and second-order leakage profile of a set of
// entities. Cooccurrences contains an entry for every unordered pair,
// including pairs whose count is zero.
type Statistics struct {
	DocumentCount int
	Unigrams      map[string]int
	Cooccurrences map[PairKey]int
}

// UnigramCount returns the number of documents containing id.
func (s Statistics) UnigramCount(id string) int { return s.Unigrams[id] }

// PairCount returns the number of documents containing both ids.
func (s Statistics) PairCount(a, b string) int {
	return s.Cooccurrences[sortedPair(a, b)]
}

// Assignment maps anonymous token IDs to keyword IDs.
type Assignment = map[string]string

// Match is one token-to-keyword correspondence.
type Match struct {
	TokenID   string
	KeywordID string
}

// Result is the outcome of a leakage analysis.
//
// Exhaustive is true only when the complete feasible-bijection space was
// searched. Fixed is populated only in that case. When Exhaustive is false,
// Witness and Mappings are still sorted, but Fixed must not be inferred from
// them.
type Result struct {
	Witness    Assignment
	Mappings   []Assignment
	Fixed      Assignment
	FixedPairs []Match

	Exhaustive      bool
	Complete        bool
	Incomplete      bool
	BudgetExhausted bool

	WitnessFound      bool
	FixedDetermined   bool
	Solutions         uint64
	MappingsTruncated bool

	TokenOrder []string

	KeywordStatistics Statistics
	TokenStatistics   Statistics
}

// Sentinel validation and analysis errors. Use errors.Is to test the error
// category.
var (
	ErrTooManyKeywords             = errors.New("netsec: more than 18 keywords")
	ErrDuplicateKeywordID          = errors.New("netsec: duplicate keyword ID")
	ErrDuplicateTokenID            = errors.New("netsec: duplicate token ID")
	ErrKeywordTokenCountMismatch   = errors.New("netsec: token count does not equal keyword count")
	ErrUnknownKeywordID            = errors.New("netsec: unknown keyword ID")
	ErrUnknownTokenID              = errors.New("netsec: unknown token ID")
	ErrNonInjectiveKnown           = errors.New("netsec: known correspondences are not injective")
	ErrKnownCorrespondenceConflict = ErrNonInjectiveKnown
	ErrStatisticConflict           = errors.New("netsec: statistics have no feasible bijection")
	ErrInvalidInput                = errors.New("netsec: invalid input")
)

// Common short aliases for the sentinel names above.
var (
	ErrDuplicateKeyword  = ErrDuplicateKeywordID
	ErrDuplicateToken    = ErrDuplicateTokenID
	ErrKnownNotInjective = ErrNonInjectiveKnown
	ErrNoFeasibleMapping = ErrStatisticConflict
)

// InputError adds context to one of the validation sentinels.
type InputError struct {
	Kind   error
	Detail string
}

func (e *InputError) Error() string {
	if e.Detail == "" {
		return e.Kind.Error()
	}
	return e.Kind.Error() + ": " + e.Detail
}

func (e *InputError) Unwrap() error { return e.Kind }

func inputError(kind error, format string, args ...any) error {
	return &InputError{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

// KeywordStatistics computes the first- and second-order profile from
// plaintext keyword sets. IDs absent from keywords are outside the analyzed
// corpus and are ignored. A repeated keyword in one document contributes once.
func KeywordStatistics(keywords []string, documents []Document) (Statistics, error) {
	if err := validateKeywordIDs(keywords); err != nil {
		return Statistics{}, err
	}
	return keywordStatistics(keywords, documents), nil
}

// TokenStatistics computes the first- and second-order profile from anonymous
// token hit sets. Duplicate IDs in one token's hit list contribute once.
func TokenStatistics(tokens []Token) (Statistics, error) {
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if _, ok := seen[token.ID]; ok {
			return Statistics{}, inputError(ErrDuplicateTokenID, "%q", token.ID)
		}
		seen[token.ID] = struct{}{}
	}
	return tokenStatistics(tokens), nil
}

func validateKeywordIDs(keywords []string) error {
	if len(keywords) > 18 {
		return inputError(ErrTooManyKeywords, "got %d", len(keywords))
	}
	seen := make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		if _, ok := seen[keyword]; ok {
			return inputError(ErrDuplicateKeywordID, "%q", keyword)
		}
		seen[keyword] = struct{}{}
	}
	return nil
}

func sortedPair(a, b string) PairKey {
	if a < b {
		return PairKey{a, b}
	}
	return PairKey{b, a}
}

func keywordStatistics(keywords []string, documents []Document) Statistics {
	stats := Statistics{
		DocumentCount: len(documents),
		Unigrams:      make(map[string]int, len(keywords)),
		Cooccurrences: make(map[PairKey]int, len(keywords)*(len(keywords)-1)/2),
	}
	known := make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		stats.Unigrams[keyword] = 0
		known[keyword] = struct{}{}
	}
	for i := 0; i < len(keywords); i++ {
		for j := i + 1; j < len(keywords); j++ {
			stats.Cooccurrences[sortedPair(keywords[i], keywords[j])] = 0
		}
	}

	for _, document := range documents {
		set := make(map[string]struct{}, len(document))
		for _, keyword := range document {
			if _, ok := known[keyword]; ok {
				set[keyword] = struct{}{}
			}
		}
		ids := make([]string, 0, len(set))
		for keyword := range set {
			ids = append(ids, keyword)
		}
		sort.Strings(ids)
		for _, keyword := range ids {
			stats.Unigrams[keyword]++
		}
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				stats.Cooccurrences[sortedPair(ids[i], ids[j])]++
			}
		}
	}
	return stats
}

func tokenStatistics(tokens []Token) Statistics {
	stats := Statistics{
		Unigrams:      make(map[string]int, len(tokens)),
		Cooccurrences: make(map[PairKey]int, len(tokens)*(len(tokens)-1)/2),
	}
	for _, token := range tokens {
		stats.Unigrams[token.ID] = 0
	}
	for i := 0; i < len(tokens); i++ {
		for j := i + 1; j < len(tokens); j++ {
			stats.Cooccurrences[sortedPair(tokens[i].ID, tokens[j].ID)] = 0
		}
	}

	docIDs := make(map[string]int)
	sets := make([]map[int]struct{}, len(tokens))
	for i, token := range tokens {
		set := make(map[int]struct{}, len(token.Documents))
		for _, documentID := range token.Documents {
			index, ok := docIDs[documentID]
			if !ok {
				index = len(docIDs)
				docIDs[documentID] = index
			}
			set[index] = struct{}{}
		}
		sets[i] = set
		stats.Unigrams[token.ID] = len(set)
	}
	for i := 0; i < len(tokens); i++ {
		for j := i + 1; j < len(tokens); j++ {
			stats.Cooccurrences[sortedPair(tokens[i].ID, tokens[j].ID)] =
				intersectionSize(sets[i], sets[j])
		}
	}
	stats.DocumentCount = len(docIDs)
	return stats
}

func intersectionSize(a, b map[int]struct{}) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	count := 0
	for item := range a {
		if _, ok := b[item]; ok {
			count++
		}
	}
	return count
}
