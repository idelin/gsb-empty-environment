package csp

// NewTenVariableDemoProblem returns the acceptance scenario described in the
// README. Variable x8 has no legal value while x2 is 0, but that conflict is
// independent of variables x3 through x7.
func NewTenVariableDemoProblem() Problem {
	return Problem{
		Variables: []Variable{
			{Name: "x1", Domain: []int{0}},
			{Name: "x2", Domain: []int{0, 1}},
			{Name: "x3", Domain: sequentialDomain(25)},
			{Name: "x4", Domain: sequentialDomain(25)},
			{Name: "x5", Domain: sequentialDomain(25)},
			{Name: "x6", Domain: sequentialDomain(25)},
			{Name: "x7", Domain: sequentialDomain(25)},
			{Name: "x8", Domain: []int{0, 1}},
			{Name: "x9", Domain: []int{0}},
			{Name: "x10", Domain: []int{0}},
		},
		Constraints: []BinaryConstraint{
			{
				Left:  1,
				Right: 7,
				Allow: func(x2, x8 int) bool {
					return x2 == 1
				},
			},
		},
	}
}

func sequentialDomain(size int) []int {
	domain := make([]int, size)
	for value := range domain {
		domain[value] = value
	}
	return domain
}
