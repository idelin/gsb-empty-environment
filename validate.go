package netsec

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// Reference kind describing how a <secret> element points at a registry entry.
const (
	// RefUUID means the reference uses the uuid attribute.
	RefUUID = "uuid"
	// RefUsage means the reference uses the usage attribute.
	RefUsage = "usage"
)

// SecretRef is one <secret> reference found in the domain XML.
type SecretRef struct {
	// Kind is RefUUID or RefUsage.
	Kind string
	// UUID holds the uuid attribute when Kind == RefUUID.
	UUID string
	// Usage holds the usage attribute when Kind == RefUsage.
	Usage string
	// Type is the libvirt secret usage type (e.g. "ceph", "iscsi", "luks").
	Type string
	// Path identifies where the reference appeared, for error messages, for
	// example "disk[0]/auth/secret" or "disk[1]/encryption/secret".
	Path string
}

// xmlSecret maps a libvirt <secret> element.
type xmlSecret struct {
	XMLName xml.Name `xml:"secret"`
	Type    string   `xml:"type,attr"`
	UUID    string   `xml:"uuid,attr"`
	Usage   string   `xml:"usage,attr"`
}

// xmlAuth maps a disk <auth> element.
type xmlAuth struct {
	XMLName xml.Name  `xml:"auth"`
	Secret  xmlSecret `xml:"secret"`
}

// xmlEncryption maps a disk <encryption> element.
type xmlEncryption struct {
	XMLName xml.Name  `xml:"encryption"`
	Secret  xmlSecret `xml:"secret"`
}

// xmlDisk maps a <disk> element, capturing only the relevant children.
type xmlDisk struct {
	XMLName    xml.Name       `xml:"disk"`
	Auth       *xmlAuth       `xml:"auth"`
	Encryption *xmlEncryption `xml:"encryption"`
}

// xmlDomain maps the subset of domain XML that carries secret references plus
// the domain UUID. The domain UUID is parsed deliberately so the code can
// treat it as a separate namespace and never feed it into a secret lookup.
type xmlDomain struct {
	XMLName xml.Name  `xml:"domain"`
	UUID    string    `xml:"uuid"`
	Disks   []xmlDisk `xml:"devices>disk"`
}

// ParseDomainXML parses a domain XML fragment and returns the domain UUID plus
// every secret reference nested under a disk <auth> or <encryption> element,
// in document order. A fragment without an enclosing <domain> is accepted.
func ParseDomainXML(data []byte) (domainUUID string, refs []SecretRef, err error) {
	var d xmlDomain
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if err = decoder.Decode(&d); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrMalformedXML, err)
	}
	for diskIndex, disk := range d.Disks {
		if disk.Auth != nil {
			refs = append(refs, buildRef(disk.Auth.Secret, fmt.Sprintf("disk[%d]/auth/secret", diskIndex)))
		}
		if disk.Encryption != nil {
			refs = append(refs, buildRef(disk.Encryption.Secret, fmt.Sprintf("disk[%d]/encryption/secret", diskIndex)))
		}
	}
	return d.UUID, refs, nil
}

// buildRef converts an XML secret into a SecretRef. An empty <secret/>
// element is kept with an empty Kind so ValidateDomainSecrets can flag it at
// the source path instead of silently ignoring a dangling reference.
func buildRef(xs xmlSecret, path string) SecretRef {
	ref := SecretRef{Type: xs.Type, Path: path}
	switch {
	case xs.UUID != "" && xs.Usage != "":
		ref.Kind = RefUUID
		ref.UUID = xs.UUID
		ref.Usage = xs.Usage
	case xs.UUID != "":
		ref.Kind = RefUUID
		ref.UUID = xs.UUID
	case xs.Usage != "":
		ref.Kind = RefUsage
		ref.Usage = xs.Usage
	}
	return ref
}

// ValidateDomainSecrets is the pre-define / pre-start entry point. It parses
// the domain XML fragment and authorizes every embedded secret reference for
// the given tenant against the registry.
//
// The domain UUID is never used as a secret identifier: even when a domain UUID
// equals a secret UUID, the reference is still resolved solely through the
// registry and the tenant ownership rules. On success it returns the resolved,
// value-free descriptors in reference order.
func (r *Registry) ValidateDomainSecrets(xmlFragment []byte, tenantID string) ([]SecretInfo, error) {
	domainUUID, refs, err := ParseDomainXML(xmlFragment)
	if err != nil {
		return nil, err
	}
	_ = domainUUID // intentionally unused: domain UUID and secret UUID are distinct namespaces.

	resolved := make([]SecretInfo, 0, len(refs))
	for _, ref := range refs {
		if ref.Kind == "" {
			return nil, fmt.Errorf("%w: %s: secret requires a uuid or usage attribute", ErrInvalidReference, ref.Path)
		}
		if ref.Kind == RefUUID && ref.UUID != "" && ref.Usage != "" {
			return nil, fmt.Errorf("%w: %s: uuid and usage are mutually exclusive", ErrInvalidReference, ref.Path)
		}
		info, err := r.Resolve(ref, tenantID)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", err, ref.Path)
		}
		resolved = append(resolved, info)
	}
	return resolved, nil
}
