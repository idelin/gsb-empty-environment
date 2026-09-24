package canonicalkey

type Pair struct {
	Name       string
	Left       Request
	Right      Request
	Equivalent bool
}

type PairCheck struct {
	Pair      Pair
	LeftKey   string
	RightKey  string
	Collision bool
	Diverged  bool
}

func GeneratePairs() []Pair {
	return standardPairs()
}

func StandardPairs() []Pair {
	return standardPairs()
}

func standardPairs() []Pair {
	return []Pair{
		{
			Name:       "numeric one versus string one",
			Equivalent: false,
			Left:       Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Number("1"))},
			Right:      Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams("1")},
		},
		{
			Name:       "missing params versus explicit null",
			Equivalent: false,
			Left:       Request{"tenant-a", "search", PresentString("cfg-3"), AbsentParams()},
			Right:      Request{"tenant-a", "search", PresentString("cfg-3"), NullParams()},
		},
		{
			Name:       "object field order is irrelevant",
			Equivalent: true,
			Left: Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Object{
				"a": Number("1"),
				"b": Number("2"),
			})},
			Right: Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Object{
				"b": Number("2"),
				"a": Number("1"),
			})},
		},
		{
			Name:       "array order is preserved",
			Equivalent: false,
			Left:       Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Array{Number("1"), Number("2")})},
			Right:      Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Array{Number("2"), Number("1")})},
		},
		{
			Name:       "configuration versions are isolated",
			Equivalent: false,
			Left:       Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Object{"mode": "strict"})},
			Right:      Request{"tenant-a", "search", PresentString("cfg-4"), PresentParams(Object{"mode": "strict"})},
		},
		{
			Name:       "tenants are isolated",
			Equivalent: false,
			Left:       Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Number("1"))},
			Right:      Request{"tenant-b", "search", PresentString("cfg-3"), PresentParams(Number("1"))},
		},
		{
			Name:       "parameter types are isolated",
			Equivalent: false,
			Left:       Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Number("1"))},
			Right:      Request{"tenant-a", "rank", PresentString("cfg-3"), PresentParams(Number("1"))},
		},
		{
			Name:       "number forms with equal mathematical value",
			Equivalent: true,
			Left:       Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Number("1.00"))},
			Right:      Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(Number("1e0"))},
		},
	}
}

type KeyFunc func(request Request, limits Limits) (string, []byte, error)

func CanonicalKeyFunc(request Request, limits Limits) (string, []byte, error) {
	canonical, err := CanonicalRequest(request, limits)
	if err != nil {
		return "", nil, err
	}
	return DigestKey(canonical, DefaultHash), canonical, nil
}

func CheckPairs(pairs []Pair, limits Limits) ([]PairCheck, error) {
	return CheckPairsWithKeyFunc(pairs, limits, CanonicalKeyFunc)
}

func CheckPairsWithKeyFunc(pairs []Pair, limits Limits, keyFunc KeyFunc) ([]PairCheck, error) {
	if keyFunc == nil {
		keyFunc = CanonicalKeyFunc
	}
	checks := make([]PairCheck, 0, len(pairs))
	for _, pair := range pairs {
		leftKey, _, err := keyFunc(pair.Left, limits)
		if err != nil {
			return nil, err
		}
		rightKey, _, err := keyFunc(pair.Right, limits)
		if err != nil {
			return nil, err
		}
		sameKey := leftKey == rightKey
		checks = append(checks, PairCheck{
			Pair:      pair,
			LeftKey:   leftKey,
			RightKey:  rightKey,
			Collision: sameKey && !pair.Equivalent,
			Diverged:  !sameKey && pair.Equivalent,
		})
	}
	return checks, nil
}

func Counterexamples(checks []PairCheck) []PairCheck {
	var result []PairCheck
	for _, check := range checks {
		if check.Collision || check.Diverged {
			result = append(result, check)
		}
	}
	return result
}
