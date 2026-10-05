package algnum

import "fmt"

// NumJSON is the JSON wire form of an algebraic number: ascending-degree
// integer coefficients and rational interval endpoints ("p/q" or "p").
type NumJSON struct {
	Coeffs []string `json:"coeffs"`
	Lo     string   `json:"lo"`
	Hi     string   `json:"hi"`
}

// Request asks for a comparison of lhs against rhs.
type Request struct {
	Lhs NumJSON `json:"lhs"`
	Rhs NumJSON `json:"rhs"`
}

// Response carries the comparison result and its certificate, or an error.
type Response struct {
	Relation    string       `json:"relation,omitempty"`
	Certificate *Certificate `json:"certificate,omitempty"`
	Error       string       `json:"error,omitempty"`
}

// CheckRequest asks the standalone checker to verify a certificate.
type CheckRequest struct {
	Request     Request     `json:"request"`
	Certificate Certificate `json:"certificate"`
}

// CheckResponse reports whether the certificate verified.
type CheckResponse struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

// Parse converts the wire form into a Num. It enforces integer
// coefficients but does not validate the isolating interval (Validate
// does that, and Compare/Verify call it).
func (n NumJSON) Parse() (Num, error) {
	p, err := ParseIntPoly(n.Coeffs)
	if err != nil {
		return Num{}, err
	}
	lo, err := parseRat(n.Lo)
	if err != nil {
		return Num{}, fmt.Errorf("lower endpoint: %w", err)
	}
	hi, err := parseRat(n.Hi)
	if err != nil {
		return Num{}, fmt.Errorf("upper endpoint: %w", err)
	}
	return Num{P: p, Lo: lo, Hi: hi}, nil
}

// Parse converts both sides of a comparison request.
func (r Request) Parse() (Num, Num, error) {
	a, err := r.Lhs.Parse()
	if err != nil {
		return Num{}, Num{}, fmt.Errorf("lhs: %w", err)
	}
	b, err := r.Rhs.Parse()
	if err != nil {
		return Num{}, Num{}, fmt.Errorf("rhs: %w", err)
	}
	return a, b, nil
}
