package netsec_test

import (
	"errors"
	"reflect"
	"testing"

	netsec "example.org/netsec/q0041"
)

func TestExhaustiveUniqueMappingAndFixedCorrespondences(t *testing.T) {
	input := netsec.Input{
		Keywords: []string{"a", "b", "c"},
		Documents: [][]string{
			{"a", "b", "c"},
			{"a", "b"},
			{"a"},
		},
		Tokens: []netsec.Token{
			{ID: "t1", Documents: []string{"d1", "d2"}},
			{ID: "t2", Documents: []string{"d1"}},
			{ID: "t3", Documents: []string{"d1", "d2", "d3"}},
		},
	}

	result, err := netsec.AnalyzeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Exhaustive || !result.Complete || result.Incomplete {
		t.Fatalf("search should be exhaustive: %+v", result)
	}
	want := netsec.Assignment{"t1": "b", "t2": "c", "t3": "a"}
	if !reflect.DeepEqual(result.Witness, want) {
		t.Fatalf("witness = %#v, want %#v", result.Witness, want)
	}
	if !reflect.DeepEqual(result.Fixed, want) {
		t.Fatalf("fixed = %#v, want %#v", result.Fixed, want)
	}
	if result.Solutions != 1 || len(result.Mappings) != 1 {
		t.Fatalf("solutions = %d, mappings = %d", result.Solutions, len(result.Mappings))
	}
}

func TestSymmetricKeywordsDuplicateDocumentsAndZeroHits(t *testing.T) {
	input := netsec.Input{
		Keywords: []string{"m", "n", "z"},
		Documents: [][]string{
			{"m", "n"},
			{"m", "n", "m"},
			{"outside-the-keyword-set"},
		},
		Tokens: []netsec.Token{
			{ID: "t1", Documents: []string{"d1", "d2", "d1"}},
			{ID: "t2", Documents: []string{"d1", "d2"}},
			{ID: "t3"},
		},
	}

	result, err := netsec.AnalyzeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Exhaustive || result.Solutions != 2 {
		t.Fatalf("want two exhaustive solutions, got %+v", result)
	}
	if result.KeywordStatistics.Unigrams["m"] != 2 ||
		result.KeywordStatistics.Unigrams["n"] != 2 ||
		result.KeywordStatistics.Cooccurrences[netsec.PairKey{"m", "n"}] != 2 {
		t.Fatalf("duplicate docs/keywords were not counted as sets: %+v", result.KeywordStatistics)
	}

	wantMappings := []netsec.Assignment{
		{"t1": "m", "t2": "n", "t3": "z"},
		{"t1": "n", "t2": "m", "t3": "z"},
	}
	if !reflect.DeepEqual(result.Mappings, wantMappings) {
		t.Fatalf("mappings = %#v, want %#v", result.Mappings, wantMappings)
	}
	if len(result.Fixed) != 1 || result.Fixed["t3"] != "z" {
		t.Fatalf("only the zero-hit token should be fixed: %#v", result.Fixed)
	}
}

func TestOnlyFirstAndSecondOrderLeakageIsUsed(t *testing.T) {
	keywords := []string{"x", "y", "z"}
	// x,y,z form a triangle. Their pair frequencies are all one.
	documents := [][]string{
		{"x", "y"},
		{"x", "z"},
		{"x"},
		{"y", "z"},
	}

	// Both token sets have frequencies 3,2,2 and every pair frequency is 1.
	// Their three-way overlaps differ: 0 in the first case and 1 in the second.
	first := netsec.Input{
		Keywords:  keywords,
		Documents: documents,
		Tokens: []netsec.Token{
			{ID: "a", Documents: []string{"1", "2", "3"}},
			{ID: "b", Documents: []string{"1", "4"}},
			{ID: "c", Documents: []string{"2", "4"}},
		},
	}
	second := netsec.Input{
		Keywords:  keywords,
		Documents: documents,
		Tokens: []netsec.Token{
			{ID: "a", Documents: []string{"1", "2", "3"}},
			{ID: "b", Documents: []string{"1", "4"}},
			{ID: "c", Documents: []string{"1", "5"}},
		},
	}

	firstResult, err := netsec.AnalyzeInput(first)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := netsec.AnalyzeInput(second)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(
		firstResult.TokenStatistics.Cooccurrences,
		secondResult.TokenStatistics.Cooccurrences,
	) {
		t.Fatalf(
			"test setup must have equal pair statistics: %#v vs %#v",
			firstResult.TokenStatistics,
			secondResult.TokenStatistics,
		)
	}
	if !reflect.DeepEqual(firstResult.Mappings, secondResult.Mappings) ||
		!reflect.DeepEqual(firstResult.Fixed, secondResult.Fixed) {
		t.Fatalf(
			"higher-order overlap changed the result: %#v vs %#v",
			firstResult,
			secondResult,
		)
	}
	want := []netsec.Assignment{
		{"a": "x", "b": "y", "c": "z"},
		{"a": "x", "b": "z", "c": "y"},
	}
	if !reflect.DeepEqual(firstResult.Mappings, want) {
		t.Fatalf("mappings = %#v, want %#v", firstResult.Mappings, want)
	}
	if len(firstResult.Fixed) != 1 || firstResult.Fixed["a"] != "x" {
		t.Fatalf("fixed = %#v, want only a->x", firstResult.Fixed)
	}
}

func TestMappingCapAndCompleteSort(t *testing.T) {
	keywords := []string{"k1", "k2", "k3", "k4", "k5", "k6"}
	tokens := make([]netsec.Token, len(keywords))
	for i := range tokens {
		tokens[i] = netsec.Token{ID: "t" + string(rune('1'+i)), Documents: []string{"d1", "d2"}}
	}
	input := netsec.Input{
		Keywords: keywords,
		Documents: [][]string{
			{"k1", "k2", "k3", "k4", "k5", "k6"},
			{"k1", "k2", "k3", "k4", "k5", "k6"},
		},
		Tokens:      tokens,
		MaxMappings: 7,
	}

	result, err := netsec.AnalyzeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Exhaustive || result.Solutions != 720 {
		t.Fatalf("want 720 exhaustive solutions, got %+v", result)
	}
	if len(result.Mappings) != 7 || !result.MappingsTruncated {
		t.Fatalf("want seven retained mappings, got %d", len(result.Mappings))
	}
	wantFirst := []string{"k1", "k2", "k3", "k4", "k5", "k6"}
	wantSecond := []string{"k1", "k2", "k3", "k4", "k6", "k5"}
	if got := orderedKeywords(result.Mappings[0], result.TokenOrder); !reflect.DeepEqual(got, wantFirst) {
		t.Fatalf("first sequence = %v, want %v", got, wantFirst)
	}
	if got := orderedKeywords(result.Mappings[1], result.TokenOrder); !reflect.DeepEqual(got, wantSecond) {
		t.Fatalf("second sequence = %v, want %v", got, wantSecond)
	}
	if len(result.Fixed) != 0 {
		t.Fatalf("fully symmetric corpus has no fixed mappings: %#v", result.Fixed)
	}
}

func TestBudgetStopsWithoutClaimingFirstWitnessIsComplete(t *testing.T) {
	keywords := []string{"k1", "k2", "k3", "k4", "k5", "k6"}
	tokens := make([]netsec.Token, len(keywords))
	for i := range tokens {
		tokens[i] = netsec.Token{ID: "t" + string(rune('1'+i)), Documents: []string{"d1"}}
	}
	input := netsec.Input{
		Keywords:     keywords,
		Documents:    [][]string{{"k1", "k2", "k3", "k4", "k5", "k6"}},
		Tokens:       tokens,
		SearchBudget: 6,
	}

	result, err := netsec.AnalyzeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Exhaustive || !result.Incomplete || !result.BudgetExhausted {
		t.Fatalf("budget exhaustion must be explicit: %+v", result)
	}
	if !result.WitnessFound || result.Witness == nil || len(result.Mappings) != 1 {
		t.Fatalf("expected the one complete witness found by the budget: %+v", result)
	}
	if result.FixedDetermined || len(result.Fixed) != 0 {
		t.Fatalf("must not infer fixed mappings from the first witness: %+v", result.Fixed)
	}
}

func TestTokenAndKeywordIDsUseByteOrder(t *testing.T) {
	input := netsec.Input{
		Keywords:  []string{"k2", "k10"},
		Documents: [][]string{{"k2", "k10"}},
		Tokens: []netsec.Token{
			{ID: "t2", Documents: []string{"d1"}},
			{ID: "t10", Documents: []string{"d1"}},
		},
	}

	result, err := netsec.AnalyzeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"t10", "t2"}
	if !reflect.DeepEqual(result.TokenOrder, wantOrder) {
		t.Fatalf("token order = %v, want %v", result.TokenOrder, wantOrder)
	}
	wantMappings := []netsec.Assignment{
		{"t10": "k10", "t2": "k2"},
		{"t10": "k2", "t2": "k10"},
	}
	if !reflect.DeepEqual(result.Mappings, wantMappings) {
		t.Fatalf("mappings = %#v, want %#v", result.Mappings, wantMappings)
	}
}

func TestNonIsomorphicPairGraphsConflict(t *testing.T) {
	input := netsec.Input{
		Keywords: []string{"a", "b", "c", "d", "e", "f"},
		Documents: [][]string{
			{"a", "b"},
			{"b", "c"},
			{"c", "a"},
			{"d", "e"},
			{"e", "f"},
			{"f", "d"},
		},
		Tokens: []netsec.Token{
			{ID: "t1", Documents: []string{"p1", "p6"}},
			{ID: "t2", Documents: []string{"p1", "p2"}},
			{ID: "t3", Documents: []string{"p2", "p3"}},
			{ID: "t4", Documents: []string{"p3", "p4"}},
			{ID: "t5", Documents: []string{"p4", "p5"}},
			{ID: "t6", Documents: []string{"p5", "p6"}},
		},
	}

	_, err := netsec.AnalyzeInput(input)
	if !errors.Is(err, netsec.ErrStatisticConflict) {
		t.Fatalf("want statistic conflict, got %v", err)
	}
}

func TestValidationAndKnownCorrespondenceConflicts(t *testing.T) {
	base := netsec.Input{
		Keywords: []string{"a", "b"},
		Documents: [][]string{
			{"a"},
			{"a", "b"},
		},
		Tokens: []netsec.Token{
			{ID: "t1", Documents: []string{"d1"}},
			{ID: "t2", Documents: []string{"d1", "d2"}},
		},
	}

	tests := []struct {
		name   string
		change func(*netsec.Input)
		want   error
	}{
		{
			name: "too many keywords",
			change: func(in *netsec.Input) {
				in.Keywords = make([]string, 19)
				in.Tokens = nil
			},
			want: netsec.ErrTooManyKeywords,
		},
		{
			name: "duplicate keyword",
			change: func(in *netsec.Input) {
				in.Keywords = []string{"a", "a"}
			},
			want: netsec.ErrDuplicateKeywordID,
		},
		{
			name: "duplicate token",
			change: func(in *netsec.Input) {
				in.Tokens[1].ID = "t1"
			},
			want: netsec.ErrDuplicateTokenID,
		},
		{
			name: "token count",
			change: func(in *netsec.Input) {
				in.Tokens = in.Tokens[:1]
			},
			want: netsec.ErrKeywordTokenCountMismatch,
		},
		{
			name: "one token to two keywords",
			change: func(in *netsec.Input) {
				in.Known = []netsec.KnownMatch{
					{TokenID: "t2", KeywordID: "a"},
					{TokenID: "t2", KeywordID: "b"},
				}
			},
			want: netsec.ErrNonInjectiveKnown,
		},
		{
			name: "two tokens to one keyword",
			change: func(in *netsec.Input) {
				in.Known = []netsec.KnownMatch{
					{TokenID: "t2", KeywordID: "a"},
					{TokenID: "t1", KeywordID: "a"},
				}
			},
			want: netsec.ErrNonInjectiveKnown,
		},
		{
			name: "known statistics conflict",
			change: func(in *netsec.Input) {
				in.Known = []netsec.KnownMatch{{TokenID: "t1", KeywordID: "a"}}
			},
			want: netsec.ErrStatisticConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.Keywords = append([]string(nil), base.Keywords...)
			input.Documents = append([][]string(nil), base.Documents...)
			input.Tokens = append([]netsec.Token(nil), base.Tokens...)
			test.change(&input)
			_, err := netsec.AnalyzeInput(input)
			if !errors.Is(err, test.want) {
				t.Fatalf("want %v, got %v", test.want, err)
			}
		})
	}
}

func TestKnownInjectiveConstraintIsApplied(t *testing.T) {
	input := netsec.Input{
		Keywords: []string{"m", "n"},
		Documents: [][]string{
			{"m", "n"},
			{"m", "n"},
		},
		Tokens: []netsec.Token{
			{ID: "t1", Documents: []string{"d1", "d2"}},
			{ID: "t2", Documents: []string{"d1", "d2"}},
		},
		Known: []netsec.KnownMatch{{TokenID: "t2", KeywordID: "n"}},
	}
	result, err := netsec.AnalyzeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Solutions != 1 || result.Fixed["t2"] != "n" || result.Fixed["t1"] != "m" {
		t.Fatalf("known constraint was not preserved: %+v", result)
	}
}

func orderedKeywords(assignment netsec.Assignment, tokenOrder []string) []string {
	sequence := make([]string, len(tokenOrder))
	for i, tokenID := range tokenOrder {
		sequence[i] = assignment[tokenID]
	}
	return sequence
}
