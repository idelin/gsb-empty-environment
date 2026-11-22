package redemption

import "encoding/json"

// EncodeProblem serializes a problem to canonical JSON for import/export.
func EncodeProblem(p Problem) ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// DecodeProblem parses an exported problem.
func DecodeProblem(data []byte) (Problem, error) {
	var p Problem
	err := json.Unmarshal(data, &p)
	return p, err
}

// EncodeSolution serializes a solution including its optimality certificate.
func EncodeSolution(s *Solution) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// DecodeSolution parses an exported solution.
func DecodeSolution(data []byte) (*Solution, error) {
	var s Solution
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
