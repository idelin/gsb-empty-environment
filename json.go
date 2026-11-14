package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// AuthorizeJSON is the JSON form used by a gateway that forwards raw search
// bodies. Empty input is treated as an empty search object.
func (a *Authorizer) AuthorizeJSON(expression string, body []byte) (*Plan, []byte, error) {
	request, err := decodeRequest(body)
	if err != nil {
		return nil, nil, err
	}
	plan, sanitized, authErr := a.Prepare(expression, request)
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return plan, nil, err
	}
	return plan, encoded, authErr
}

// FilterResponseJSON filters a raw backend response.
func (a *Authorizer) FilterResponseJSON(plan *Plan, body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("netsec: empty response")
	}
	var response map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil {
		return nil, err
	}
	filtered := a.FilterResponse(plan, response)
	return json.Marshal(filtered)
}

// AuthorizeDocumentJSON filters a raw document being indexed through an alias.
func (a *Authorizer) AuthorizeDocumentJSON(alias string, body []byte) (*WriteTarget, []byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil, errors.New("netsec: empty document")
	}
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, err
	}
	target, filtered, err := a.AuthorizeDocument(alias, document)
	encoded, marshalErr := json.Marshal(filtered)
	if marshalErr != nil {
		return target, nil, marshalErr
	}
	return target, encoded, err
}

func decodeRequest(body []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	var request map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	if request == nil {
		request = map[string]any{}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("netsec: multiple JSON values")
		}
		return nil, err
	}
	return request, nil
}
