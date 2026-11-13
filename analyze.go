package netsec

import (
	"fmt"
	"sort"
)

// AnalyzeInput is the statically typed analysis entry point.
func AnalyzeInput(input Input, options ...Options) (Result, error) {
	return analyze(input, firstOptions(options))
}

// AnalyzeLeakage is an alias for Analyze.
func AnalyzeLeakage(args ...any) (Result, error) { return Analyze(args...) }

// RecoverKeywords is an alias for Analyze.
func RecoverKeywords(args ...any) (Result, error) { return Analyze(args...) }

// Analyze accepts either an Input, or the positional form:
//
//	Analyze(keywords []string, documents [][]string, tokens []Token,
//	        known []KnownMatch, options ...Options)
//
// Known and options may be omitted. The flexible form is a convenience for
// test harnesses; AnalyzeInput is preferred for new code.
func Analyze(args ...any) (Result, error) {
	input, options, err := parseAnalyzeArgs(args)
	if err != nil {
		return Result{}, err
	}
	return analyze(input, options)
}

func firstOptions(options []Options) Options {
	if len(options) == 0 {
		return Options{}
	}
	return options[0]
}

func parseAnalyzeArgs(args []any) (Input, Options, error) {
	if len(args) == 0 {
		return Input{}, Options{}, inputError(ErrInvalidInput, "no input")
	}
	if len(args) == 1 {
		switch input := args[0].(type) {
		case Input:
			return input, Options{}, nil
		case *Input:
			if input == nil {
				return Input{}, Options{}, inputError(ErrInvalidInput, "nil *Input")
			}
			return *input, Options{}, nil
		}
	}

	options := Options{}
	last := len(args)
	if last > 0 {
		switch value := args[last-1].(type) {
		case Options:
			options = value
			last--
		case *Options:
			if value != nil {
				options = *value
			}
			last--
		}
	}

	if last == 1 {
		switch input := args[0].(type) {
		case Input:
			return input, options, nil
		case *Input:
			if input == nil {
				return Input{}, Options{}, inputError(ErrInvalidInput, "nil *Input")
			}
			return *input, options, nil
		}
	}

	if last < 3 || last > 4 {
		return Input{}, Options{}, inputError(
			ErrInvalidInput,
			"want keywords, documents, tokens, and optional known matches",
		)
	}

	keywords, ok := args[0].([]string)
	if !ok {
		return Input{}, Options{}, inputError(ErrInvalidInput, "keywords must be []string")
	}
	documents, err := normalizeDocuments(args[1])
	if err != nil {
		return Input{}, Options{}, err
	}
	tokens, err := normalizeTokens(args[2])
	if err != nil {
		return Input{}, Options{}, err
	}
	var known []KnownMatch
	if last == 4 {
		known, err = normalizeKnown(args[3])
		if err != nil {
			return Input{}, Options{}, err
		}
	}
	return Input{
		Keywords:  keywords,
		Documents: documents,
		Tokens:    tokens,
		Known:     known,
	}, options, nil
}

func normalizeDocuments(value any) ([]Document, error) {
	switch documents := value.(type) {
	case [][]string:
		return documents, nil
	case map[string][]string:
		result := make([]Document, 0, len(documents))
		for _, document := range documents {
			result = append(result, document)
		}
		return result, nil
	default:
		return nil, inputError(
			ErrInvalidInput,
			"documents must be [][]string or a document-ID map",
		)
	}
}

func normalizeTokens(value any) ([]Token, error) {
	switch tokens := value.(type) {
	case []Token:
		return tokens, nil
	case map[string][]string:
		result := make([]Token, 0, len(tokens))
		for id, documents := range tokens {
			result = append(result, Token{ID: id, Documents: documents})
		}
		return result, nil
	case map[string]map[string]struct{}:
		result := make([]Token, 0, len(tokens))
		for id, documents := range tokens {
			hitDocs := make([]string, 0, len(documents))
			for document := range documents {
				hitDocs = append(hitDocs, document)
			}
			result = append(result, Token{ID: id, Documents: hitDocs})
		}
		return result, nil
	default:
		return nil, inputError(
			ErrInvalidInput,
			"tokens must be []Token or a token-ID map",
		)
	}
}

func normalizeKnown(value any) ([]KnownMatch, error) {
	switch known := value.(type) {
	case nil:
		return nil, nil
	case []KnownMatch:
		return known, nil
	case map[string]string:
		result := make([]KnownMatch, 0, len(known))
		for tokenID, keywordID := range known {
			result = append(result, KnownMatch{TokenID: tokenID, KeywordID: keywordID})
		}
		return result, nil
	case [][2]string:
		result := make([]KnownMatch, 0, len(known))
		for _, pair := range known {
			result = append(result, KnownMatch{TokenID: pair[0], KeywordID: pair[1]})
		}
		return result, nil
	default:
		return nil, inputError(
			ErrInvalidInput,
			"known matches must be []KnownMatch or map[string]string",
		)
	}
}

type indexedToken struct {
	id         string
	documents  map[int]struct{}
	unigram    int
	incident   []int
	profileKey string
	known      int
	candidates []int
}

type indexedKeyword struct {
	id         string
	unigram    int
	incident   []int
	profileKey string
}

type analysis struct {
	input        Input
	options      Options
	keywords     []indexedKeyword
	tokens       []indexedToken
	edges        [][]int
	keywordEdges [][]int
	order        []int
	budget       uint64
	stopped      bool
}

func analyze(input Input, options Options) (Result, error) {
	if err := validateKeywordIDs(input.Keywords); err != nil {
		return Result{}, err
	}
	if len(input.Tokens) != len(input.Keywords) {
		return Result{}, inputError(
			ErrKeywordTokenCountMismatch,
			"got %d tokens and %d keywords",
			len(input.Tokens),
			len(input.Keywords),
		)
	}
	tokenSeen := make(map[string]struct{}, len(input.Tokens))
	for _, token := range input.Tokens {
		if _, ok := tokenSeen[token.ID]; ok {
			return Result{}, inputError(ErrDuplicateTokenID, "%q", token.ID)
		}
		tokenSeen[token.ID] = struct{}{}
	}

	keywordStats := keywordStatistics(input.Keywords, input.Documents)
	tokenStats := tokenStatistics(input.Tokens)

	a, err := newAnalysis(input, options, keywordStats, tokenStats)
	if err != nil {
		return Result{
			KeywordStatistics: keywordStats,
			TokenStatistics:   tokenStats,
		}, err
	}
	return a.run(keywordStats, tokenStats)
}

func newAnalysis(
	input Input,
	options Options,
	keywordStats Statistics,
	tokenStats Statistics,
) (*analysis, error) {
	maxMappings := options.MaxMappings
	if maxMappings == 0 {
		maxMappings = options.Limit
	}
	if maxMappings == 0 {
		maxMappings = input.MaxMappings
	}
	if maxMappings <= 0 || maxMappings > MaxRetainedMappings {
		maxMappings = MaxRetainedMappings
	}
	budget := options.SearchBudget
	if budget == 0 {
		budget = options.NodeBudget
	}
	if budget == 0 {
		budget = input.SearchBudget
	}
	if budget == 0 {
		budget = DefaultSearchBudget
	}
	options.MaxMappings = maxMappings
	options.SearchBudget = budget

	keywordIndex := make(map[string]int, len(input.Keywords))
	keywordIDs := append([]string(nil), input.Keywords...)
	sort.Strings(keywordIDs)
	for i, id := range keywordIDs {
		keywordIndex[id] = i
	}

	tokenIndex := make(map[string]int, len(input.Tokens))
	tokenIDs := make([]string, 0, len(input.Tokens))
	for _, token := range input.Tokens {
		tokenIDs = append(tokenIDs, token.ID)
	}
	sort.Strings(tokenIDs)
	for i, id := range tokenIDs {
		tokenIndex[id] = i
	}

	keywords := make([]indexedKeyword, len(keywordIDs))
	for i, id := range keywordIDs {
		keywords[i].id = id
		keywords[i].unigram = keywordStats.Unigrams[id]
	}

	tokens := make([]indexedToken, len(tokenIDs))
	for i, id := range tokenIDs {
		tokens[i].id = id
		tokens[i].unigram = tokenStats.Unigrams[id]
		tokens[i].known = -1
	}

	docIDs := make(map[string]int)
	for i := range tokens {
		raw := input.Tokens[tokenIndex[tokens[i].id]]
		tokens[i].documents = make(map[int]struct{}, len(raw.Documents))
		for _, documentID := range raw.Documents {
			index, ok := docIDs[documentID]
			if !ok {
				index = len(docIDs)
				docIDs[documentID] = index
			}
			tokens[i].documents[index] = struct{}{}
		}
	}

	edges := make([][]int, len(tokens))
	for i := range edges {
		edges[i] = make([]int, len(tokens))
	}
	for i := 0; i < len(tokens); i++ {
		for j := i + 1; j < len(tokens); j++ {
			value := intersectionSize(tokens[i].documents, tokens[j].documents)
			edges[i][j] = value
			edges[j][i] = value
		}
	}

	keywordEdges := make([][]int, len(keywords))
	for i := range keywordEdges {
		keywordEdges[i] = make([]int, len(keywords))
	}
	for i := 0; i < len(keywords); i++ {
		for j := i + 1; j < len(keywords); j++ {
			value := keywordStats.Cooccurrences[sortedPair(keywords[i].id, keywords[j].id)]
			keywordEdges[i][j] = value
			keywordEdges[j][i] = value
		}
	}

	for i := range tokens {
		tokens[i].incident = incidentWeights(edges[i], i)
		tokens[i].profileKey = profileKey(tokens[i].unigram, tokens[i].incident)
	}
	for i := range keywords {
		keywords[i].incident = incidentWeights(keywordEdges[i], i)
		keywords[i].profileKey = profileKey(keywords[i].unigram, keywords[i].incident)
	}

	knownByToken := make(map[int]int, len(input.Known))
	knownByKeyword := make(map[int]int, len(input.Known))
	resolvedKnown := make([]struct {
		token   int
		keyword int
	}, 0, len(input.Known))
	for _, match := range input.Known {
		tokenAt, ok := tokenIndex[match.TokenID]
		if !ok {
			return nil, inputError(ErrUnknownTokenID, "%q", match.TokenID)
		}
		keywordAt, ok := keywordIndex[match.KeywordID]
		if !ok {
			return nil, inputError(ErrUnknownKeywordID, "%q", match.KeywordID)
		}
		if previous, ok := knownByToken[tokenAt]; ok {
			if previous == keywordAt {
				continue
			}
			return nil, inputError(
				ErrNonInjectiveKnown,
				"token %q maps to both %q and %q",
				match.TokenID,
				keywordIDs[previous],
				match.KeywordID,
			)
		}
		if previous, ok := knownByKeyword[keywordAt]; ok {
			return nil, inputError(
				ErrNonInjectiveKnown,
				"keyword %q is mapped from both %q and %q",
				match.KeywordID,
				tokenIDs[previous],
				match.TokenID,
			)
		}
		knownByToken[tokenAt] = keywordAt
		knownByKeyword[keywordAt] = tokenAt
		resolvedKnown = append(resolvedKnown, struct {
			token   int
			keyword int
		}{tokenAt, keywordAt})
	}

	// Validate statistics only after the entire relation is known to be a
	// partial injection, so a non-injective input is reported as such.
	for _, match := range resolvedKnown {
		if tokens[match.token].profileKey != keywords[match.keyword].profileKey {
			return nil, inputError(
				ErrStatisticConflict,
				"known %q -> %q has incompatible leakage statistics",
				tokens[match.token].id,
				keywords[match.keyword].id,
			)
		}
		tokens[match.token].known = match.keyword
	}

	for i := range tokens {
		if tokens[i].known >= 0 {
			tokens[i].candidates = []int{tokens[i].known}
			continue
		}
		for j := range keywords {
			if tokens[i].profileKey != keywords[j].profileKey {
				continue
			}
			if _, used := knownByKeyword[j]; !used {
				tokens[i].candidates = append(tokens[i].candidates, j)
			}
		}
		if len(tokens[i].candidates) == 0 {
			return nil, inputError(
				ErrStatisticConflict,
				"token %q has no keyword with matching first- and second-order statistics",
				tokens[i].id,
			)
		}
	}

	order := make([]int, len(tokens))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		left := tokens[order[i]]
		right := tokens[order[j]]
		leftKnown := left.known >= 0
		rightKnown := right.known >= 0
		if leftKnown != rightKnown {
			return leftKnown
		}
		if len(left.candidates) != len(right.candidates) {
			return len(left.candidates) < len(right.candidates)
		}
		return left.id < right.id
	})

	return &analysis{
		input:        input,
		options:      options,
		keywords:     keywords,
		tokens:       tokens,
		edges:        edges,
		keywordEdges: keywordEdges,
		order:        order,
		budget:       budget,
	}, nil
}

func incidentWeights(row []int, self int) []int {
	incident := make([]int, 0, len(row)-1)
	for i, value := range row {
		if i != self {
			incident = append(incident, value)
		}
	}
	sort.Ints(incident)
	return incident
}

func profileKey(unigram int, incident []int) string {
	key := fmt.Sprintf("%d|", unigram)
	for _, value := range incident {
		key += fmt.Sprintf("%d,", value)
	}
	return key
}

func (a *analysis) run(keywordStats, tokenStats Statistics) (Result, error) {
	result := Result{
		TokenOrder:        make([]string, len(a.tokens)),
		KeywordStatistics: keywordStats,
		TokenStatistics:   tokenStats,
	}
	for i := range a.tokens {
		result.TokenOrder[i] = a.tokens[i].id
	}

	recovered := a.search()
	result.Solutions = recovered.solutions
	result.Exhaustive = !a.stopped
	result.Complete = result.Exhaustive
	result.Incomplete = !result.Exhaustive
	result.BudgetExhausted = !result.Exhaustive
	result.FixedDetermined = result.Exhaustive

	result.Mappings = make([]Assignment, 0, len(recovered.found))
	for _, solution := range recovered.found {
		result.Mappings = append(
			result.Mappings,
			solutionToAssignment(solution, a.tokens, a.keywords),
		)
	}
	sortAssignments(result.Mappings, result.TokenOrder)
	if len(result.Mappings) > 0 {
		result.Witness = result.Mappings[0]
		result.WitnessFound = true
	}
	result.MappingsTruncated = recovered.truncated

	if result.Exhaustive {
		result.Fixed = recovered.fixed
		result.FixedPairs = fixedPairs(recovered.fixed, result.TokenOrder)
	}
	if result.Exhaustive && recovered.solutions == 0 {
		return result, inputError(
			ErrStatisticConflict,
			"first- and second-order statistics admit no bijection",
		)
	}
	return result, nil
}

type searchResult struct {
	found     [][]int
	fixed     Assignment
	solutions uint64
	truncated bool
}

func (a *analysis) search() searchResult {
	result := searchResult{
		found: make([][]int, 0, a.options.MaxMappings),
	}
	if len(a.tokens) == 0 {
		result.found = append(result.found, []int{})
		result.fixed = Assignment{}
		result.solutions = 1
		return result
	}

	assigned := make([]int, len(a.tokens))
	used := make([]bool, len(a.keywords))
	for i := range assigned {
		assigned[i] = -1
	}

	var visit func(depth int) bool
	visit = func(depth int) bool {
		if depth == len(a.order) {
			solution := append([]int(nil), assigned...)
			if result.fixed == nil {
				result.fixed = solutionToAssignment(solution, a.tokens, a.keywords)
			} else {
				updateFixed(result.fixed, solution, a.tokens, a.keywords)
			}
			insertSolution(&result.found, solution, a.options.MaxMappings)
			result.solutions++
			return true
		}

		tokenAt := a.order[depth]
		for _, keywordAt := range a.tokens[tokenAt].candidates {
			if used[keywordAt] {
				continue
			}
			if !a.partialCompatible(tokenAt, keywordAt, assigned) {
				continue
			}
			if a.budget == 0 {
				a.stopped = true
				return false
			}
			a.budget--
			assigned[tokenAt] = keywordAt
			used[keywordAt] = true
			if !visit(depth + 1) {
				return false
			}
			used[keywordAt] = false
			assigned[tokenAt] = -1
		}
		return true
	}

	if !visit(0) {
		result.truncated = true
	}
	if result.solutions > uint64(len(result.found)) {
		result.truncated = true
	}
	return result
}

func (a *analysis) partialCompatible(tokenAt, keywordAt int, assigned []int) bool {
	for otherToken, otherKeyword := range assigned {
		if otherKeyword < 0 || otherToken == tokenAt {
			continue
		}
		if a.edges[tokenAt][otherToken] != a.keywordEdges[keywordAt][otherKeyword] {
			return false
		}
	}
	return true
}

func solutionToAssignment(
	solution []int,
	tokens []indexedToken,
	keywords []indexedKeyword,
) Assignment {
	assignment := make(Assignment, len(tokens))
	for i, keyword := range solution {
		assignment[tokens[i].id] = keywords[keyword].id
	}
	return assignment
}

func updateFixed(
	fixed Assignment,
	solution []int,
	tokens []indexedToken,
	keywords []indexedKeyword,
) {
	for i, keyword := range solution {
		tokenID := tokens[i].id
		if current, ok := fixed[tokenID]; ok && current != keywords[keyword].id {
			delete(fixed, tokenID)
		}
	}
}

func insertSolution(found *[][]int, solution []int, limit int) {
	position := sort.Search(len(*found), func(i int) bool {
		return solutionLess(solution, (*found)[i])
	})
	if position >= limit {
		return
	}
	(*found) = append(*found, nil)
	copy((*found)[position+1:], (*found)[position:])
	(*found)[position] = solution
	if len(*found) > limit {
		(*found) = (*found)[:limit]
	}
}

func solutionLess(left, right []int) bool {
	for i := range left {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return false
}

func fixedPairs(fixed Assignment, tokenOrder []string) []Match {
	if len(fixed) == 0 {
		return nil
	}
	result := make([]Match, 0, len(fixed))
	for _, tokenID := range tokenOrder {
		if keywordID, ok := fixed[tokenID]; ok {
			result = append(result, Match{TokenID: tokenID, KeywordID: keywordID})
		}
	}
	return result
}

func sortAssignments(assignments []Assignment, tokenOrder []string) {
	sort.SliceStable(assignments, func(i, j int) bool {
		left := assignments[i]
		right := assignments[j]
		for _, tokenID := range tokenOrder {
			leftKeyword := left[tokenID]
			rightKeyword := right[tokenID]
			if leftKeyword != rightKeyword {
				return leftKeyword < rightKeyword
			}
		}
		return false
	})
}
