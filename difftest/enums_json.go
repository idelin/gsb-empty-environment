package difftest

import (
	"encoding/json"
	"fmt"
)

var outcomeKindNames = map[string]OutcomeKind{
	"value":     OutcomeValue,
	"run_error": OutcomeRunError,
	"timeout":   OutcomeTimeout,
}

func (k OutcomeKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

func (k *OutcomeKind) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, ok := outcomeKindNames[s]
	if !ok {
		return fmt.Errorf("unknown outcome kind %q", s)
	}
	*k = v
	return nil
}

var verdictKindNames = map[string]VerdictKind{
	"agree":          VerdictAgree,
	"semantic_diff":  VerdictSemanticDiff,
	"timeout_diff":   VerdictTimeoutDiff,
	"both_timeout":   VerdictBothTimeout,
	"run_error_diff": VerdictRunErrorDiff,
	"both_run_error": VerdictBothRunError,
	"parse_error":    VerdictParseError,
}

func (k VerdictKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

func (k *VerdictKind) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, ok := verdictKindNames[s]
	if !ok {
		return fmt.Errorf("unknown verdict kind %q", s)
	}
	*k = v
	return nil
}
