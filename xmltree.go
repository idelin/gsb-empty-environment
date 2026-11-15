package netsec

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

type xmlNode struct {
	name     xml.Name
	attrs    []xml.Attr
	children []*xmlNode
	text     strings.Builder
}

func parseXMLTree(doc []byte) (*xmlNode, error) {
	doc = bytes.TrimPrefix(doc, []byte{0xef, 0xbb, 0xbf})
	decoder := xml.NewDecoder(bytes.NewReader(doc))
	decoder.Strict = true

	var (
		root  *xmlNode
		stack []*xmlNode
	)

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedXML, err)
		}

		switch token := token.(type) {
		case xml.StartElement:
			node := &xmlNode{
				name:  token.Name,
				attrs: append([]xml.Attr(nil), token.Attr...),
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("%w: multiple root elements", ErrMalformedXML)
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)

		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("%w: unexpected closing element", ErrMalformedXML)
			}
			stack = stack[:len(stack)-1]

		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(token)) != "" {
					return nil, fmt.Errorf("%w: text outside root element", ErrMalformedXML)
				}
				continue
			}
			stack[len(stack)-1].text.Write(token)

		case xml.Directive:
			return nil, fmt.Errorf("%w: DTD/directive is not allowed", ErrMalformedXML)
		}
	}

	if root == nil {
		return nil, fmt.Errorf("%w: empty document", ErrMalformedXML)
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("%w: unclosed element", ErrMalformedXML)
	}
	return root, nil
}

func directChildren(node *xmlNode, namespace, local string) []*xmlNode {
	var matches []*xmlNode
	for _, child := range node.children {
		if child.name.Space == namespace && child.name.Local == local {
			matches = append(matches, child)
		}
	}
	return matches
}

func descendants(node *xmlNode, namespace, local string) []*xmlNode {
	var matches []*xmlNode
	var visit func(*xmlNode)
	visit = func(current *xmlNode) {
		for _, child := range current.children {
			if child.name.Space == namespace && child.name.Local == local {
				matches = append(matches, child)
			}
			visit(child)
		}
	}
	visit(node)
	return matches
}

func leafText(node *xmlNode) (string, error) {
	if len(node.children) != 0 {
		return "", errors.New("expected text-only element")
	}
	value := strings.TrimSpace(node.text.String())
	if value == "" {
		return "", errors.New("element is empty")
	}
	return value, nil
}

func countIDAttributes(node *xmlNode) int {
	count := 0
	for _, attr := range node.attrs {
		if attr.Name.Space == wsuNamespace && attr.Name.Local == "Id" {
			count++
		}
	}
	return count
}
