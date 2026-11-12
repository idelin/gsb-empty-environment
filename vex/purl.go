package vex

import (
	"errors"
	"net/url"
	"strings"
)

// purl is the subset of a Package URL (https://github.com/package-url/purl-spec)
// needed to decide whether a VEX affects entry points at a component.
type purl struct {
	Type      string
	Namespace string
	Name      string
	Version   string
}

// parsePurl parses strings of the form
// "pkg:type/namespace/name@version?qualifiers#subpath". Qualifiers and
// subpath are accepted but ignored during component matching.
func parsePurl(raw string) (purl, error) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "pkg:") {
		return purl{}, errors.New("purl must start with pkg: " + raw)
	}
	body := strings.TrimPrefix(s, "pkg:")

	if hash := strings.IndexByte(body, '#'); hash >= 0 {
		body = body[:hash]
	}
	if q := strings.IndexByte(body, '?'); q >= 0 {
		body = body[:q]
	}

	slash := strings.IndexByte(body, '/')
	if slash < 0 {
		return purl{}, errors.New("purl is missing name: " + raw)
	}
	typeName := body[:slash]
	path := body[slash+1:]
	if typeName == "" || path == "" {
		return purl{}, errors.New("purl type and name are required: " + raw)
	}

	version := ""
	if at := strings.LastIndexByte(path, '@'); at >= 0 {
		version = path[at+1:]
		path = path[:at]
	}

	segments := strings.Split(path, "/")
	for i, seg := range segments {
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			return purl{}, err
		}
		segments[i] = decoded
	}
	name := segments[len(segments)-1]
	namespace := strings.Join(segments[:len(segments)-1], "/")
	if name == "" {
		return purl{}, errors.New("purl name is empty: " + raw)
	}

	return purl{
		Type:      strings.ToLower(typeName),
		Namespace: namespace,
		Name:      name,
		Version:   version,
	}, nil
}

// sameComponent reports whether two purls identify the same package
// coordinate, ignoring version, qualifiers and subpath.
func (p purl) sameComponent(other purl) bool {
	return p.Type == other.Type &&
		strings.EqualFold(p.Namespace, other.Namespace) &&
		strings.EqualFold(p.Name, other.Name)
}
