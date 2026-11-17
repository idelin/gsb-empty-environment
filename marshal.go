package netsec

import "encoding/json"

// specKnownFields is the set of fields decoded into typed WorkloadSpec
// fields. Everything else is retained in Extra so legacy or future fields
// cannot be silently dropped during normalization.
var specKnownFields = map[string]struct{}{
	"image":        {},
	"replicas":     {},
	"env":          {},
	"labels":       {},
	"identity":     {},
	"security":     {},
	"capabilities": {},
}

// UnmarshalJSON keeps unknown spec fields (such as the deprecated
// serviceAccount alias) available to the normalizer.
func (s *WorkloadSpec) UnmarshalJSON(b []byte) error {
	type alias WorkloadSpec
	var decoded alias
	if err := json.Unmarshal(b, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	extra := map[string]any{}
	for key, value := range raw {
		if _, known := specKnownFields[key]; known {
			continue
		}
		var unmarshaled any
		if err := json.Unmarshal(value, &unmarshaled); err != nil {
			return err
		}
		extra[key] = unmarshaled
	}
	*s = WorkloadSpec(decoded)
	if len(extra) > 0 {
		s.Extra = extra
	}
	return nil
}

// MarshalJSON emits retained unknown fields alongside the typed fields.
func (s WorkloadSpec) MarshalJSON() ([]byte, error) {
	type alias WorkloadSpec
	b, err := json.Marshal(alias(s))
	if err != nil {
		return nil, err
	}
	if len(s.Extra) == 0 {
		return b, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	for key, value := range s.Extra {
		out[key] = value
	}
	return json.Marshal(out)
}
