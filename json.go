package netsec

import (
	"encoding/json"
	"fmt"
)

// Presentation is the JSON shape accepted by VerifyPresentationJSON.
//
// A commitment element may be a digest string or an object of the form
// {"...": "<digest>"}. Disclosures may be indexed envelopes, encoded strings,
// or literal [salt, value] arrays.
type Presentation struct {
	Commitment  []any        `json:"commitment"`
	Disclosures []Disclosure `json:"disclosures"`
}

// VerifyPresentationJSON verifies a JSON presentation and returns the
// reconstructed array.
func VerifyPresentationJSON(data []byte) ([]any, error) {
	var presentation Presentation
	if err := json.Unmarshal(data, &presentation); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDisclosure, err)
	}
	commitment, err := commitmentStrings(presentation.Commitment)
	if err != nil {
		return nil, err
	}
	return VerifyArray(commitment, presentation.Disclosures)
}

// VerifyJSON is an alias for VerifyPresentationJSON.
func VerifyJSON(data []byte) ([]any, error) {
	return VerifyPresentationJSON(data)
}

// VerifyDocument verifies a commitment JSON array and a disclosures JSON
// array, each supplied separately.
func VerifyDocument(commitmentJSON, disclosuresJSON []byte) ([]any, error) {
	var rawCommitment []any
	if err := json.Unmarshal(commitmentJSON, &rawCommitment); err != nil {
		return nil, fmt.Errorf("%w: commitment: %v", ErrInvalidCommitment, err)
	}
	commitment, err := commitmentStrings(rawCommitment)
	if err != nil {
		return nil, err
	}

	var disclosures []Disclosure
	if err := json.Unmarshal(disclosuresJSON, &disclosures); err != nil {
		return nil, fmt.Errorf("%w: disclosures: %v", ErrInvalidDisclosure, err)
	}
	return VerifyArray(commitment, disclosures)
}

// VerifyAnyArray verifies a commitment whose elements may be digest strings
// or {"...": digest} objects.
func VerifyAnyArray(commitment []any, disclosures []Disclosure) ([]any, error) {
	digests, err := commitmentStrings(commitment)
	if err != nil {
		return nil, err
	}
	return VerifyArray(digests, disclosures)
}

func commitmentStrings(elements []any) ([]string, error) {
	commitment := make([]string, len(elements))
	for index, element := range elements {
		switch value := element.(type) {
		case string:
			commitment[index] = value
		case map[string]any:
			digest, ok := value["..."]
			if !ok {
				digest, ok = value["digest"]
			}
			if !ok {
				return nil, fmt.Errorf("%w: position %d has no digest", ErrInvalidCommitment, index)
			}
			text, ok := digest.(string)
			if !ok {
				return nil, fmt.Errorf("%w: position %d digest is not a string", ErrInvalidCommitment, index)
			}
			commitment[index] = text
		default:
			return nil, fmt.Errorf("%w: position %d has unsupported type %T", ErrInvalidCommitment, index, element)
		}
	}
	return commitment, nil
}
