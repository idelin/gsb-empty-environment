package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

func main() {
	doc := []byte(`<Response xmlns="urn:oasis:names:tc:SAML:2.0:protocol" a="1" a="2" ID="_r1"/>`)
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = true
	for {
		tok, err := dec.Token()
		if err != nil {
			fmt.Println("ERR", err)
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			fmt.Printf("START name=%+v attrs:", t.Name)
			for _, a := range t.Attr {
				fmt.Printf(" {%s|%s=%s}", a.Name.Space, a.Name.Local, a.Value)
			}
			fmt.Println()
		case xml.EndElement:
			fmt.Printf("END %s\n", t.Name.Local)
	case xml.CharData:
			fmt.Printf("TEXT %q\n", string(t))
		case xml.Comment:
			fmt.Printf("COMMENT %q\n", string(t))
		case xml.ProcInst:
			fmt.Printf("PI %s %q\n", t.Target, string(t.Inst))
		case xml.Directive:
			fmt.Printf("DIRECTIVE %q\n", string(t))
		}
	}
}
