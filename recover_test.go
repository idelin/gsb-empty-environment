package netsec_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	netsec "example.org/netsec/q0024"
)

type referenceField int

func (p referenceField) norm(value int) int {
	value %= int(p)
	if value < 0 {
		value += int(p)
	}
	return value
}

func (p referenceField) add(a, b int) int {
	return p.norm(a + b)
}

func (p referenceField) sub(a, b int) int {
	return p.norm(a - b)
}

func (p referenceField) mul(a, b int) int {
	return p.norm(a * b)
}

func (p referenceField) inv(a int) int {
	base := p.norm(a)
	result := 1
	exponent := int(p) - 2
	for exponent > 0 {
		if exponent&1 == 1 {
			result = p.mul(result, base)
		}
		base = p.mul(base, base)
		exponent >>= 1
	}
	return result
}

func (p referenceField) eval(coefficients []int, x int) int {
	result := 0
	for i := len(coefficients) - 1; i >= 0; i-- {
		result = p.add(p.mul(result, x), coefficients[i])
	}
	return result
}

func referenceInterpolate(field referenceField, points []netsec.Share, k int) []int {
	coefficients := make([]int, k)
	for i, point := range points {
		basis := make([]int, k)
		basis[0] = 1
		degree := 0
		denominator := 1
		for j, other := range points {
			if i == j {
				continue
			}
			next := make([]int, k)
			for d := 0; d <= degree; d++ {
				next[d] = field.add(next[d], field.mul(field.sub(0, other.X), basis[d]))
				next[d+1] = field.add(next[d+1], basis[d])
			}
			basis = next
			degree++
			denominator = field.mul(denominator, field.sub(point.X, other.X))
		}
		scale := field.mul(point.Y, field.inv(denominator))
		for d := 0; d < k; d++ {
			coefficients[d] = field.add(coefficients[d], field.mul(scale, basis[d]))
		}
	}
	return coefficients
}

func exhaustiveSubsetReference(shares []netsec.Share, p, k, t int) [][]int {
	field := referenceField(p)
	n := len(shares)
	threshold := n - t
	candidates := make(map[string][]int)
	selection := make([]int, k)

	var enumerate func(start, depth int)
	enumerate = func(start, depth int) {
		if depth == k {
			points := make([]netsec.Share, k)
			for i, index := range selection {
				points[i] = shares[index]
			}
			polynomial := referenceInterpolate(field, points, k)
			matches := 0
			for _, share := range shares {
				if field.eval(polynomial, share.X) == field.norm(share.Y) {
					matches++
				}
			}
			if matches >= threshold {
				key := fmt.Sprint(polynomial)
				candidates[key] = polynomial
			}
			return
		}
		for index := start; index <= n-(k-depth); index++ {
			selection[depth] = index
			enumerate(index+1, depth+1)
		}
	}
	enumerate(0, 0)

	result := make([][]int, 0, len(candidates))
	for _, polynomial := range candidates {
		result = append(result, polynomial)
	}
	return result
}

func randomPolynomial(random *rand.Rand, p, k int) []int {
	coefficients := make([]int, k)
	for i := range coefficients {
		coefficients[i] = random.Intn(p)
	}
	return coefficients
}

func randomCoordinates(random *rand.Rand, p, n int) []int {
	coordinates := make([]int, 0, n)
	seen := make(map[int]struct{}, n)
	for len(coordinates) < n {
		x := 1 + random.Intn(p-1)
		if _, exists := seen[x]; exists {
			continue
		}
		seen[x] = struct{}{}
		coordinates = append(coordinates, x)
	}
	return coordinates
}

func makeShares(polynomial []int, coordinates []int, p int) []netsec.Share {
	field := referenceField(p)
	shares := make([]netsec.Share, len(coordinates))
	for i, x := range coordinates {
		shares[i] = netsec.Share{
			ID: fmt.Sprintf("share-%d", i),
			X:  x,
			Y:  field.eval(polynomial, x),
		}
	}
	return shares
}

func corruptShares(random *rand.Rand, shares []netsec.Share, count, p int) []int {
	positions := random.Perm(len(shares))[:count]
	for _, position := range positions {
		delta := 1 + random.Intn(p-1)
		shares[position].Y = (shares[position].Y + delta) % p
	}
	slices.Sort(positions)
	return positions
}

func inconsistentIndices(result *netsec.Result) []int {
	return slices.Clone(result.InconsistentIndices)
}

func TestRecoverAgainstExhaustiveSubsetReference(t *testing.T) {
	random := rand.New(rand.NewSource(20261109))
	primes := []int{11, 17, 23}

	for trial := 0; trial < 120; trial++ {
		p := primes[trial%len(primes)]
		k := 1 + random.Intn(4)
		errorBudget := random.Intn(3)
		n := k + 2*errorBudget + random.Intn(2)
		if n > p-1 {
			n = p - 1
		}
		if n < k+2*errorBudget {
			continue
		}

		polynomial := randomPolynomial(random, p, k)
		coordinates := randomCoordinates(random, p, n)
		shares := makeShares(polynomial, coordinates, p)
		injected := random.Intn(errorBudget + 1)
		badPositions := corruptShares(random, shares, injected, p)

		reference := exhaustiveSubsetReference(shares, p, k, errorBudget)
		if len(reference) != 1 {
			t.Fatalf("trial %d: reference found %d candidates", trial, len(reference))
		}

		result, err := netsec.Recover(shares, p, k, errorBudget)
		if err != nil {
			t.Fatalf("trial %d: Recover failed: %v", trial, err)
		}
		if !slices.Equal(result.Polynomial, reference[0]) {
			t.Fatalf("trial %d: polynomial = %v, reference = %v", trial, result.Polynomial, reference[0])
		}
		if result.Secret != reference[0][0] {
			t.Fatalf("trial %d: secret = %d, reference = %d", trial, result.Secret, reference[0][0])
		}
		if got := inconsistentIndices(result); !slices.Equal(got, badPositions) {
			t.Fatalf("trial %d: inconsistent = %v, injected = %v", trial, got, badPositions)
		}
		if result.ConsistentCount != n-injected {
			t.Fatalf("trial %d: consistent count = %d, want %d", trial, result.ConsistentCount, n-injected)
		}
	}
}

func TestFewerErrorsThanBudgetAndExtraLocatorFactors(tt *testing.T) {
	p := 17
	k := 3
	t := 3
	n := k + 2*t
	polynomial := []int{5, 2, 1}
	coordinates := randomCoordinates(rand.New(rand.NewSource(7)), p, n)
	shares := makeShares(polynomial, coordinates, p)

	for errorsInjected := 0; errorsInjected <= t; errorsInjected++ {
		candidate := slices.Clone(shares)
		badPositions := corruptShares(rand.New(rand.NewSource(int64(100+errorsInjected))), candidate, errorsInjected, p)
		result, err := netsec.Recover(candidate, p, k, t)
		if err != nil {
			tt.Fatalf("%d errors: Recover failed: %v", errorsInjected, err)
		}
		if !slices.Equal(result.Polynomial, polynomial) {
			tt.Fatalf("%d errors: polynomial = %v", errorsInjected, result.Polynomial)
		}
		if got := inconsistentIndices(result); !slices.Equal(got, badPositions) {
			tt.Fatalf("%d errors: inconsistent = %v, injected = %v", errorsInjected, got, badPositions)
		}
	}
}

func TestMaximumParameters(tt *testing.T) {
	p := 65521
	k := 16
	t := 12
	n := 40
	polynomial := randomPolynomial(rand.New(rand.NewSource(99)), p, k)

	for _, errorsInjected := range []int{0, 1, t - 1, t} {
		coordinates := randomCoordinates(rand.New(rand.NewSource(31)), p, n)
		shares := makeShares(polynomial, coordinates, p)
		badPositions := corruptShares(rand.New(rand.NewSource(int64(200+errorsInjected))), shares, errorsInjected, p)

		result, err := netsec.Recover(shares, p, k, t)
		if err != nil {
			tt.Fatalf("%d errors: Recover failed: %v", errorsInjected, err)
		}
		if !slices.Equal(result.Polynomial, polynomial) {
			tt.Fatalf("%d errors: recovered the wrong polynomial", errorsInjected)
		}
		if got := inconsistentIndices(result); !slices.Equal(got, badPositions) {
			tt.Fatalf("%d errors: inconsistent = %v, injected = %v", errorsInjected, got, badPositions)
		}
	}
}

func TestRejectsWhenNoGlobalDegreeBoundedPolynomialFits(tt *testing.T) {
	p := 101
	k := 3
	t := 1
	shares := []netsec.Share{
		{ID: "a", X: 1, Y: 1},
		{ID: "b", X: 2, Y: 2},
		{ID: "c", X: 3, Y: 0},
		{ID: "d", X: 4, Y: 0},
		{ID: "e", X: 5, Y: 0},
	}

	if candidates := exhaustiveSubsetReference(shares, p, k, t); len(candidates) != 0 {
		tt.Fatalf("reference unexpectedly found candidates: %v", candidates)
	}
	if _, err := netsec.Recover(shares, p, k, t); !errors.Is(err, netsec.ErrNoConsistentPolynomial) {
		tt.Fatalf("Recover error = %v, want ErrNoConsistentPolynomial", err)
	}
}

func TestOverBudgetInputNearAnotherCodeword(tt *testing.T) {
	p := 17
	k := 3
	t := 1
	coordinates := []int{1, 2, 3, 4, 5}
	original := []int{7, 3, 9}
	field := referenceField(p)

	offset := make([]int, k)
	offset[0] = 1
	degree := 0
	for i := 0; i < k-1; i++ {
		next := make([]int, k)
		for d := 0; d <= degree; d++ {
			next[d] = field.add(next[d], field.mul(field.sub(0, coordinates[i]), offset[d]))
			next[d+1] = field.add(next[d+1], offset[d])
		}
		offset = next
		degree++
	}

	other := make([]int, k)
	for i := range other {
		other[i] = field.add(original[i], offset[i])
	}

	different := make([]int, 0, 2*t+1)
	for i, x := range coordinates {
		if field.eval(offset, x) != 0 {
			different = append(different, i)
		}
	}
	if len(different) != 2*t+1 {
		tt.Fatalf("difference support has size %d, want %d", len(different), 2*t+1)
	}

	received := makeShares(other, coordinates, p)
	for i := 0; i < t; i++ {
		position := different[i]
		received[position].Y = field.eval(original, coordinates[position])
	}
	inconsistentWithRecovered := slices.Clone(different[:t])
	inconsistentWithOriginal := slices.Clone(different[t:])

	candidates := exhaustiveSubsetReference(received, p, k, t)
	if len(candidates) != 1 || !slices.Equal(candidates[0], other) {
		tt.Fatalf("reference candidates = %v, want only %v", candidates, other)
	}

	result, err := netsec.Recover(received, p, k, t)
	if err != nil {
		tt.Fatalf("Recover failed: %v", err)
	}
	if !slices.Equal(result.Polynomial, other) {
		tt.Fatalf("polynomial = %v, want %v", result.Polynomial, other)
	}
	if got := inconsistentIndices(result); !slices.Equal(got, inconsistentWithRecovered) {
		tt.Fatalf("inconsistent = %v, want positions inconsistent with recovered codeword %v", got, inconsistentWithRecovered)
	}
	if len(result.InconsistentIndices) != t {
		tt.Fatalf("inconsistent count = %d, want %d", len(result.InconsistentIndices), t)
	}
	if len(inconsistentWithOriginal) != t+1 || slices.Equal(result.InconsistentIndices, inconsistentWithOriginal) {
		tt.Fatalf("decoder incorrectly attributed the original %d-share corruption", len(inconsistentWithOriginal))
	}
	if result.Secret != other[0] {
		tt.Fatalf("secret = %d, want %d", result.Secret, other[0])
	}
}

func TestRejectsInvalidRequests(t *testing.T) {
	valid := []netsec.Share{
		{ID: "a", X: 1, Y: 2},
		{ID: "b", X: 2, Y: 3},
		{ID: "c", X: 3, Y: 4},
		{ID: "d", X: 4, Y: 5},
		{ID: "e", X: 5, Y: 6},
	}

	tooMany := make([]netsec.Share, 41)
	for i := range tooMany {
		tooMany[i] = netsec.Share{X: i + 1, Y: 0}
	}

	cases := []struct {
		name    string
		shares  []netsec.Share
		p, k, t int
		want    error
	}{
		{"non-prime field", valid, 15, 3, 1, netsec.ErrInvalidField},
		{"field above bound", valid, 65537, 3, 1, netsec.ErrInvalidField},
		{"field below two", valid, 1, 3, 1, netsec.ErrInvalidField},
		{"zero k", valid, 101, 0, 1, netsec.ErrInvalidParameter},
		{"k above bound", valid, 101, 17, 0, netsec.ErrInvalidParameter},
		{"too few shares", valid[:1], 101, 2, 0, netsec.ErrInvalidParameter},
		{"too many shares", tooMany, 101, 1, 0, netsec.ErrInvalidParameter},
		{"negative t", valid[:1], 101, 1, -1, netsec.ErrInvalidParameter},
		{"distance constraint", valid[:4], 101, 3, 1, netsec.ErrDistance},
		{"zero x", []netsec.Share{{X: 0, Y: 1}}, 17, 1, 0, netsec.ErrInvalidShare},
		{"x equals p", []netsec.Share{{X: 17, Y: 1}}, 17, 1, 0, netsec.ErrInvalidShare},
		{"negative y", []netsec.Share{{X: 1, Y: -1}}, 17, 1, 0, netsec.ErrInvalidShare},
		{"y equals p", []netsec.Share{{X: 1, Y: 17}}, 17, 1, 0, netsec.ErrInvalidShare},
		{"duplicate x", []netsec.Share{{X: 1, Y: 1}, {X: 1, Y: 2}}, 17, 1, 0, netsec.ErrDuplicateX},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := netsec.Recover(test.shares, test.p, test.k, test.t); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestTinyFieldAndExactBudget(t *testing.T) {
	shares := []netsec.Share{{ID: "only", X: 1, Y: 1}}
	result, err := netsec.Recover(shares, 2, 1, 0)
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if !slices.Equal(result.Polynomial, []int{1}) || result.Secret != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestZeroPolynomial(t *testing.T) {
	shares := []netsec.Share{
		{ID: "a", X: 1, Y: 0},
		{ID: "b", X: 2, Y: 0},
		{ID: "c", X: 3, Y: 0},
		{ID: "d", X: 4, Y: 0},
	}
	result, err := netsec.Recover(shares, 17, 2, 1)
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if !slices.Equal(result.Polynomial, []int{0, 0}) || result.Secret != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.InconsistentIndices) != 0 {
		t.Fatalf("unexpected inconsistent shares: %v", result.InconsistentIndices)
	}
}
