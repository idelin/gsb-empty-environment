package netsec

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// Stanza is the part of an XML stanza that participates in source and
// destination validation.  InnerXML preserves the stanza payload.
type Stanza struct {
	XMLName  xml.Name
	ID       string `xml:"id,attr,omitempty"`
	To       string `xml:"to,attr,omitempty"`
	From     string `xml:"from,attr,omitempty"`
	Type     string `xml:"type,attr,omitempty"`
	InnerXML string `xml:",innerxml"`
}

// ParseStanza decodes exactly one XML stanza.
func ParseStanza(data []byte) (Stanza, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var st Stanza
	if err := decoder.Decode(&st); err != nil {
		return Stanza{}, fmt.Errorf("parse stanza: %w", err)
	}
	return st, nil
}
