package netsec

import "fmt"

// LogEvent contains only values observable through the callback and exact
// interval endpoints derived from those observations. It never contains a
// callback-internal plaintext or private-key value.
type LogEvent struct {
	Kind    string `json:"kind"`
	Attempt int    `json:"attempt,omitempty"`
	Step    int    `json:"step"`
	Query   string `json:"query,omitempty"`
	Result  string `json:"result,omitempty"`
	Lower   string `json:"lower,omitempty"`
	Upper   string `json:"upper,omitempty"`
}

// Logger receives observable attack progress.
type Logger func(LogEvent)

func (s *Session) emit(kind string, rec QueryRecord, result OracleResult) {
	if s.logger == nil {
		return
	}
	s.logger(LogEvent{
		Kind:    kind,
		Attempt: rec.Attempt,
		Step:    s.step,
		Query:   rec.Query,
		Result:  result.String(),
		Lower:   fmt.Sprintf("%s/2^%d", s.lo.String(), s.step),
		Upper:   fmt.Sprintf("%s/2^%d", s.hi.String(), s.step),
	})
}
