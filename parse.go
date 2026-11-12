package netsec

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// XML namespace identifiers used by WS-Security 1.1. Element and attribute
// matching is namespace-URI based, so any document prefix is accepted.
const (
	nsWSU    = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"
	nsWSSE   = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	nsSOAP11 = "http://schemas.xmlsoap.org/soap/envelope/"
	nsSOAP12 = "http://www.w3.org/2003/05/soap-envelope"
)

// timestamp carries the parsed wsu:Timestamp element.
type timestamp struct {
	id      string
	created time.Time
	expires time.Time
	// createdOK/expiresOK distinguish missing elements from zero times.
	createdOK bool
	expiresOK bool
}

// usernameToken carries the replay-relevant fields of a wsse:UsernameToken.
type usernameToken struct {
	nonce     string
	nonceOK   bool
	created   time.Time
	createdOK bool
}

type parsedMessage struct {
	timestamps  []*timestamp
	bodyID      string
	bodyPresent bool
	tokens      []usernameToken
	ids         map[string]int
}

type frameKind int

const (
	kindOther frameKind = iota
	kindSecurity
	kindTimestamp
	kindCreated
	kindExpires
	kindUsernameToken
	kindNonce
	kindUTCreated
	kindBody
)

type frame struct {
	kind frameKind
	ts   *timestamp
	ut   *usernameToken
	text strings.Builder
}

// parseMessage streams the document once, collecting every piece of state the
// validator needs. A streaming decoder lets us detect structural violations
// (duplicate timestamps, duplicate wsu:Id, multiple bodies) without binding to
// a fixed SOAP schema.
func parseMessage(r io.Reader) (*parsedMessage, error) {
	dec := xml.NewDecoder(r)

	msg := &parsedMessage{ids: make(map[string]int)}
	var stack []*frame
	depth := 0

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fail(ErrInvalidMessage, "malformed XML: %v", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			f := &frame{kind: classify(t, stack, depth)}

			// Inherit the owning element so child frames can write back without
			// relying on ordering assumptions.
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				if parent.ts != nil {
					f.ts = parent.ts
				}
				if parent.ut != nil {
					f.ut = parent.ut
				}
			}

			for _, attr := range t.Attr {
				if attr.Name.Space == nsWSU && attr.Name.Local == "Id" && attr.Value != "" {
					msg.ids[attr.Value]++
				}
			}

			switch f.kind {
			case kindTimestamp:
				ts := &timestamp{}
				ts.id = attrSpace(t, nsWSU, "Id")
				msg.timestamps = append(msg.timestamps, ts)
				f.ts = ts
			case kindBody:
				msg.bodyPresent = true
				msg.bodyID = attrSpace(t, nsWSU, "Id")
			case kindUsernameToken:
				f.ut = &usernameToken{}
			}
			stack = append(stack, f)

		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t)
			}

		case xml.EndElement:
			depth--
			if len(stack) == 0 {
				return nil, fail(ErrInvalidMessage, "unexpected closing element %q", t.Name.Local)
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			finishFrame(f, msg)
		}
	}

	if len(stack) != 0 {
		return nil, fail(ErrInvalidMessage, "unterminated XML elements")
	}
	return msg, nil
}

// classify resolves an element against its parent frame and nesting depth so
// identically named elements in unrelated namespaces cannot be confused.
func classify(start xml.StartElement, stack []*frame, depth int) frameKind {
	name := start.Name
	parent := kindOther
	if len(stack) > 0 {
		parent = stack[len(stack)-1].kind
	}

	switch {
	case name.Space == nsWSSE && name.Local == "Security" &&
		depth == 3 && parent == kindOther:
		return kindSecurity

	case name.Space == nsWSU && name.Local == "Timestamp" &&
		parent == kindSecurity:
		return kindTimestamp

	case name.Space == nsWSU && name.Local == "Created" &&
		parent == kindTimestamp:
		return kindCreated

	case name.Space == nsWSU && name.Local == "Expires" &&
		parent == kindTimestamp:
		return kindExpires

	case name.Space == nsWSSE && name.Local == "UsernameToken" &&
		parent == kindSecurity:
		return kindUsernameToken

	case name.Space == nsWSSE && name.Local == "Nonce" &&
		parent == kindUsernameToken:
		return kindNonce

	case name.Space == nsWSU && name.Local == "Created" &&
		parent == kindUsernameToken:
		return kindUTCreated

	case (name.Space == nsSOAP11 || name.Space == nsSOAP12) &&
		name.Local == "Body" && depth == 2:
		return kindBody

	default:
		return kindOther
	}
}

// finishFrame assigns accumulated character data to the owning element.
func finishFrame(f *frame, msg *parsedMessage) {
	text := strings.TrimSpace(f.text.String())

	switch f.kind {
	case kindUsernameToken:
		if f.ut != nil {
			msg.tokens = append(msg.tokens, *f.ut)
		}

	case kindCreated:
		if f.ts != nil {
			if ts, err := parseTime(text); err == nil {
				f.ts.created, f.ts.createdOK = ts, true
			}
		}

	case kindExpires:
		if f.ts != nil {
			if ts, err := parseTime(text); err == nil {
				f.ts.expires, f.ts.expiresOK = ts, true
			}
		}

	case kindNonce:
		if f.ut != nil {
			f.ut.nonce, f.ut.nonceOK = text, text != ""
		}

	case kindUTCreated:
		if f.ut != nil {
			if ts, err := parseTime(text); err == nil {
				f.ut.created, f.ut.createdOK = ts, true
			}
		}
	}
}

func attrSpace(start xml.StartElement, space, local string) string {
	for _, attr := range start.Attr {
		if attr.Name.Space == space && attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

// parseTime accepts the RFC 3339 profile mandated by WS-Security 1.1, both
// with and without fractional seconds. A timestamp without a zone offset is
// interpreted as UTC, matching common legacy gateway behavior.
func parseTime(value string) (time.Time, error) {
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, nil
	}
	if ts, err := time.Parse("2006-01-02T15:04:05.999999999", value); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time value %q", value)
}
