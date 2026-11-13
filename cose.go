package netsec

import "errors"

// ErrNotMap is returned when the protected header is not a
// definite-length CBOR map.
var ErrNotMap = errors.New("netsec: protected header is not a definite-length map")

// AdjudicateProtectedHeader is called by the verification component before
// signature verification. It decodes the COSE protected header bucket and
// rejects any encoding that uses indefinite-length arrays or maps,
// floating-point values or undefined values, because re-encoding such a
// header would change its byte boundaries and break the signature.
//
// Only a definite-length map is returned; it may proceed to signature
// verification. Any other input fails.
func AdjudicateProtectedHeader(b []byte) (map[any]any, error) {
	v, err := Decode(b)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, ErrNotMap
	}
	return m, nil
}
