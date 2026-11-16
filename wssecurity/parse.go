package wssecurity

import (
	"bytes"
	"encoding/xml"
	"io"
)

// Namespace URIs used by WS-Security 1.1 (OASIS WSS v1.1) and SOAP 1.1/1.2.
const (
	NamespaceWSSE   = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	NamespaceWSU    = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"
	NamespaceSOAP11 = "http://schemas.xmlsoap.org/soap/envelope/"
	NamespaceSOAP12 = "http://www.w3.org/2003/05/soap-envelope"
)

type timestamp struct {
	ID      string
	Created string
	Expires string
}

type usernameToken struct {
	ID      string
	Nonce   string
	Created string
}

type parsedMessage struct {
	securityHeaders int
	timestamps      []timestamp
	bodies          int
	bodyID          string
	tokens          []usernameToken
	// idCounts counts every wsu:Id occurrence in the document so duplicates
	// (including identical ids carried by unrelated elements) are caught.
	idCounts map[string]int
}

// parseMessage streams the SOAP envelope. Matching is done against namespace
// URIs rather than prefixes, so wsu:/wsse:/soap: prefixes chosen by the
// sender cannot change the outcome.
func parseMessage(r io.Reader) (*parsedMessage, error) {
	decoder := xml.NewDecoder(r)
	decoder.Strict = true

	msg := &parsedMessage{idCounts: make(map[string]int)}

	var (
		securityDepth int
		tsDepth       int
		utDepth       int
		bodyDepth     int

		inHeaderTS bool

		// Leaf element currently collecting character data, identified by its
		// local name. Timestamp and UsernameToken never nest, so one slot is
		// sufficient.
		leaf       string
		textBuffer bytes.Buffer

		currentTS timestamp
		currentUT usernameToken
	)

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, validationError(ErrMalformed, "%s", err.Error())
		}

		switch t := token.(type) {
		case xml.StartElement:
			uri, local := t.Name.Space, t.Name.Local

			if id, ok := wsuID(t.Attr); ok {
				msg.idCounts[id]++
			}

			switch {
			case uri == NamespaceWSSE && local == "Security":
				msg.securityHeaders++
				securityDepth++
			case uri == NamespaceWSU && local == "Timestamp":
				inHeaderTS = securityDepth > 0
				currentTS = timestamp{ID: attrWsuID(t.Attr)}
				tsDepth++
			case uri == NamespaceWSSE && local == "UsernameToken":
				currentUT = usernameToken{ID: attrWsuID(t.Attr)}
				utDepth++
			case isBody(uri, local):
				msg.bodies++
				msg.bodyID = attrWsuID(t.Attr)
				bodyDepth++
			}

			if tsDepth > 0 && uri == NamespaceWSU && (local == "Created" || local == "Expires") {
				leaf = local
				textBuffer.Reset()
			}
			if utDepth > 0 {
				if uri == NamespaceWSSE && local == "Nonce" {
					leaf = "Nonce"
					textBuffer.Reset()
				}
				if uri == NamespaceWSU && local == "Created" {
					leaf = "Created"
					textBuffer.Reset()
				}
			}

		case xml.CharData:
			if leaf != "" {
				textBuffer.Write(t)
			}

		case xml.EndElement:
			uri, local := t.Name.Space, t.Name.Local

			switch {
			case uri == NamespaceWSSE && local == "Security":
				if securityDepth > 0 {
					securityDepth--
				}
			case uri == NamespaceWSU && local == "Timestamp" && tsDepth > 0:
				if !inHeaderTS {
					return nil, validationError(ErrTimestampOutsideHeader, "wsu:Timestamp found outside wsse:Security")
				}
				msg.timestamps = append(msg.timestamps, currentTS)
				tsDepth--
			case uri == NamespaceWSSE && local == "UsernameToken" && utDepth > 0:
				msg.tokens = append(msg.tokens, currentUT)
				utDepth--
			case isBody(uri, local) && bodyDepth > 0:
				bodyDepth--
			}

			// Flush the leaf whose end tag is being reported.
			if leaf != "" && (local == leaf) {
				value := bytes.TrimSpace(textBuffer.Bytes())
				switch leaf {
				case "Created":
					switch {
					case tsDepth > 0:
						currentTS.Created = string(value)
					case utDepth > 0:
						currentUT.Created = string(value)
					}
				case "Expires":
					if tsDepth > 0 {
						currentTS.Expires = string(value)
					}
				case "Nonce":
					if utDepth > 0 {
						currentUT.Nonce = string(value)
					}
				}
				leaf = ""
				textBuffer.Reset()
			}
		}
	}

	return msg, nil
}

func isBody(uri, local string) bool {
	return local == "Body" && (uri == NamespaceSOAP11 || uri == NamespaceSOAP12)
}

// wsuID reports the value of a wsu:Id attribute (namespace-qualified).
func wsuID(attrs []xml.Attr) (string, bool) {
	for _, attr := range attrs {
		if attr.Name.Space == NamespaceWSU && attr.Name.Local == "Id" {
			return attr.Value, true
		}
	}
	return "", false
}

func attrWsuID(attrs []xml.Attr) string {
	if id, ok := wsuID(attrs); ok {
		return id
	}
	return ""
}

// repeatedID returns the first wsu:Id value occurring more than once.
func (m *parsedMessage) repeatedID() (string, bool) {
	for id, count := range m.idCounts {
		if count > 1 {
			return id, true
		}
	}
	return "", false
}
