package netsec

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Encoding labels attached to SecretLeak violations.
const (
	EncodingPlain  = "plain"
	EncodingBase64 = "base64"
	EncodingHex    = "hex"
)

// secretForm is one searchable representation of a cluster Secret value.
type secretForm struct {
	source   SecretValue
	encoding string
	needle   []byte
}

// buildSecretForms expands every sufficiently long secret into the plain,
// base64 and hex byte sequences that may be embedded in rendered output.
func buildSecretForms(secrets []SecretValue, minLen int) []secretForm {
	var forms []secretForm
	for _, secret := range secrets {
		if len(secret.Value) < minLen {
			continue
		}
		forms = append(forms, secretForm{secret, EncodingPlain, cloneBytes(secret.Value)})

		std := base64.StdEncoding.EncodeToString(secret.Value)
		raw := base64.RawStdEncoding.EncodeToString(secret.Value)
		urlStd := base64.URLEncoding.EncodeToString(secret.Value)
		urlRaw := base64.RawURLEncoding.EncodeToString(secret.Value)
		forms = append(forms, secretForm{secret, EncodingBase64, []byte(std)})
		if raw != std {
			forms = append(forms, secretForm{secret, EncodingBase64, []byte(raw)})
		}
		if urlStd != std {
			forms = append(forms, secretForm{secret, EncodingBase64, []byte(urlStd)})
		}
		if urlRaw != raw {
			forms = append(forms, secretForm{secret, EncodingBase64, []byte(urlRaw)})
		}

		lower := hex.EncodeToString(secret.Value)
		forms = append(forms, secretForm{secret, EncodingHex, []byte(lower)})
		upper := bytes.ToUpper([]byte(lower))
		forms = append(forms, secretForm{secret, EncodingHex, upper})
	}
	return forms
}

// scanResource checks every string field of one non-Secret resource.
func scanResource(resource *Resource, forms []secretForm) []Violation {
	var violations []Violation
	seen := make(map[string]struct{})
	for _, field := range resource.Fields {
		haystack := []byte(field.Value)
		for _, form := range forms {
			if bytes.Contains(haystack, form.needle) {
				if violation, ok := dedupeLeak(&seen, resource, field, form); ok {
					violations = append(violations, violation)
				}
				continue
			}
			// Defense in depth: if the whole field itself is an encoded
			// blob, decoding it may reveal the secret without the encoded
			// needle sharing an encoding boundary.
			if decoded, ok := decodeField(haystack, form.encoding); ok &&
				len(form.source.Value) >= DefaultMinSecretLen &&
				bytes.Contains(decoded, form.source.Value) {
				if violation, ok := dedupeLeak(&seen, resource, field, form); ok {
					violations = append(violations, violation)
				}
			}
		}
	}
	return violations
}

// dedupeLeak collapses matches from equivalent encodings of the same
// secret (e.g. padded and unpadded base64) into one violation per field.
func dedupeLeak(seen *map[string]struct{}, resource *Resource, field FieldValue, form secretForm) (Violation, bool) {
	key := strings.Join([]string{
		resource.Kind,
		resource.Namespace,
		resource.Name,
		field.Path,
		form.source.Namespace,
		form.source.Name,
		form.source.Key,
		form.encoding,
	}, "\x00")
	if _, exists := (*seen)[key]; exists {
		return Violation{}, false
	}
	(*seen)[key] = struct{}{}
	return leakViolation(resource, field, form), true
}

// decodeField attempts to interpret field as base64 or hex text.
func decodeField(field []byte, encoding string) ([]byte, bool) {
	switch encoding {
	case EncodingBase64:
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding,
			base64.RawStdEncoding,
			base64.URLEncoding,
			base64.RawURLEncoding,
		} {
			if decoded, err := enc.DecodeString(string(field)); err == nil {
				return decoded, true
			}
		}
	case EncodingHex:
		if len(field)%2 == 0 {
			if decoded, err := hex.DecodeString(string(field)); err == nil {
				return decoded, true
			}
		}
	}
	return nil, false
}

func leakViolation(resource *Resource, field FieldValue, form secretForm) Violation {
	return Violation{
		Kind:            ViolationSecretLeak,
		Message:         "field contains value of cluster Secret in " + form.encoding + " form",
		ResourceKind:    resource.Kind,
		Namespace:       resource.Namespace,
		ResourceName:    resource.Name,
		FieldPath:       field.Path,
		SecretNamespace: form.source.Namespace,
		SecretName:      form.source.Name,
		SecretKey:       form.source.Key,
		Encoding:        form.encoding,
	}
}
