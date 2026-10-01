package eval

// Public penalty weights. The score formula is intentionally simple and
// documented so results can be audited and reproduced offline.
const (
	WMissing    = 1.0  // per required slot with no mapped span (drives completeness)
	WDuplicate  = 0.5  // per extra occurrence beyond a node's Max
	WUnclosed   = 0.25 // per matched span that never received an end event
	WMisplaced  = 0.75 // per span whose name is expected but not under this parent
	WUnexpected = 0.25 // per span whose name appears nowhere in the expected graph

	// RootCap caps the score when the expected root span is missing:
	// no amount of descendant spans can compensate for a missing root.
	RootCap = 10.0
)

// FormulaText is the public, human-readable score definition.
const FormulaText = `score formula (public):

  R     = number of required slots in the chosen minimum-cost mapping
          (sum of Min over active span rules; clamped to >= 1)
  score = 100 * clamp( matched/R
                       - (0.5*duplicates + 0.25*unclosed
                          + 0.75*misplaced + 0.25*unexpected) / R,
                       0, 1 )

  * matched counts only unique required slots; duplicates NEVER raise it,
    so structural errors cannot be offset by sheer span quantity.
  * unclosed spans are matched-but-unfinished; they are NOT missing.
  * if the expected root span is missing the trace is CRITICAL and
    score = min(score, 10).
  * if the enumeration budget is exhausted the verdict is INDETERMINATE
    and no numeric score is emitted.`

// Counts is the outcome of a minimum-cost mapping between expected slots
// and actual spans.
type Counts struct {
	RequiredTotal int `json:"required_total"`
	Matched       int `json:"matched"`
	Missing       int `json:"missing"`
	Duplicates    int `json:"duplicates"`
	Unclosed      int `json:"unclosed"`
	Misplaced     int `json:"misplaced"`
	Unexpected    int `json:"unexpected"`
}

// Add merges another Counts into c.
func (c Counts) Add(o Counts) Counts {
	return Counts{
		RequiredTotal: c.RequiredTotal + o.RequiredTotal,
		Matched:       c.Matched + o.Matched,
		Missing:       c.Missing + o.Missing,
		Duplicates:    c.Duplicates + o.Duplicates,
		Unclosed:      c.Unclosed + o.Unclosed,
		Misplaced:     c.Misplaced + o.Misplaced,
		Unexpected:    c.Unexpected + o.Unexpected,
	}
}

// Score applies the public formula to a mapping outcome.
func Score(c Counts, rootMissing bool) float64 {
	denom := float64(c.RequiredTotal)
	if denom < 1 {
		denom = 1
	}
	penalty := WDuplicate*float64(c.Duplicates) +
		WUnclosed*float64(c.Unclosed) +
		WMisplaced*float64(c.Misplaced) +
		WUnexpected*float64(c.Unexpected)
	s := 100 * (float64(c.Matched)/denom - penalty/denom)
	if s < 0 {
		s = 0
	}
	if s > 100 {
		s = 100
	}
	if rootMissing && s > RootCap {
		s = RootCap
	}
	return s
}
