package netsec

import "strings"

// Article is the part of an article relevant to authorization and command
// replies. Headers is a case-preserving multi-value header map.
type Article struct {
	MessageID string
	Headers   map[string][]string
	Body      []byte
}

// Header returns the first value of a case-insensitively named header.
func (a *Article) Header(name string) string {
	if a == nil {
		return ""
	}
	for key, values := range a.Headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// HeaderValues returns all values of a case-insensitively named header.
func (a *Article) HeaderValues(name string) []string {
	if a == nil {
		return nil
	}
	for key, values := range a.Headers {
		if strings.EqualFold(key, name) {
			return values
		}
	}
	return nil
}

// Clone returns a deep copy of the article headers and body.
func (a *Article) Clone() *Article {
	if a == nil {
		return nil
	}
	clone := &Article{
		MessageID: a.MessageID,
		Headers:   make(map[string][]string, len(a.Headers)),
	}
	for key, values := range a.Headers {
		copied := make([]string, len(values))
		copy(copied, values)
		clone.Headers[key] = copied
	}
	if a.Body != nil {
		clone.Body = make([]byte, len(a.Body))
		copy(clone.Body, a.Body)
	}
	return clone
}

// Xref returns the article's Xref header value.
func (a *Article) Xref() string {
	return a.Header("Xref")
}
