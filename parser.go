package netsec

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type xmlChecksum struct {
	XMLName xml.Name
	Value   string `xml:"value,attr"`
}

type xmlArtifact struct {
	Name      string           `xml:"name,attr"`
	Checksums []xmlChecksum    `xml:",any"`
	AlsoTrust *xmlAlsoTrustSet `xml:"also-trust"`
}

type xmlAlsoTrustSet struct {
	Checksums []xmlChecksum `xml:",any"`
}

type xmlComponent struct {
	Group     string        `xml:"group,attr"`
	Name      string        `xml:"name,attr"`
	Version   string        `xml:"version,attr"`
	Artifacts []xmlArtifact `xml:"artifact"`
}

type xmlTrust struct {
	Group     *string          `xml:"group,attr"`
	Name      *string          `xml:"name,attr"`
	Version   *string          `xml:"version,attr"`
	File      *string          `xml:"file,attr"`
	Regex     bool             `xml:"regex,attr"`
	AlsoTrust *xmlAlsoTrustSet `xml:"also-trust"`
}

type xmlTrustedArtifacts struct {
	Trust []xmlTrust `xml:"trust"`
}

type xmlConfiguration struct {
	TrustedArtifacts *xmlTrustedArtifacts `xml:"trusted-artifacts"`
	AlsoTrust        *xmlAlsoTrustSet     `xml:"also-trust"`
}

type xmlMetadata struct {
	Components      []xmlComponent       `xml:"components>component"`
	Configuration   *xmlConfiguration    `xml:"configuration"`
	LegacyArtifacts *xmlTrustedArtifacts `xml:"trusted-artifacts"`
}

var primaryChecksums = map[string]bool{
	"sha1":    true,
	"sha256":  true,
	"sha512":  true,
	"md5":     true,
	"sha-256": true,
	"sha-512": true,
	"sha-1":   true,
}

// ParseMetadata reads a Gradle verification-metadata.xml document.
//
// Malformed XML is returned as an error. Individual oddities that do not
// prevent parsing (for example an unknown checksum element) are accumulated in
// Metadata.ParseErrors instead.
func ParseMetadata(r io.Reader) (*Metadata, error) {
	decoder := xml.NewDecoder(r)
	var raw xmlMetadata
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse verification metadata: %w", err)
	}

	meta := &Metadata{}
	for _, comp := range raw.Components {
		parsed := Component{
			Group:   comp.Group,
			Name:    comp.Name,
			Version: comp.Version,
		}
		for _, art := range comp.Artifacts {
			checksums := parseChecksums(art.Checksums, &meta.ParseErrors)
			if art.AlsoTrust != nil {
				for _, also := range art.AlsoTrust.Checksums {
					alg := strings.ToLower(also.XMLName.Local)
					if !primaryChecksums[alg] {
						meta.ParseErrors = append(meta.ParseErrors, fmt.Sprintf(
							"unknown also-trust checksum element <%s>", also.XMLName.Local))
					}
					checksums = append(checksums, Checksum{
						Algorithm: alg,
						Value:     also.Value,
						AlsoTrust: true,
					})
				}
			}
			parsed.Artifacts = append(parsed.Artifacts, Artifact{
				Name:      art.Name,
				Checksums: checksums,
			})
		}
		meta.Components = append(meta.Components, parsed)
	}

	trusted := raw.LegacyArtifacts
	if raw.Configuration != nil {
		trusted = raw.Configuration.TrustedArtifacts
	}
	if trusted == nil {
		// no trusted-artifacts block in either location
	}
	if raw.Configuration != nil && raw.Configuration.AlsoTrust != nil {
		for _, also := range raw.Configuration.AlsoTrust.Checksums {
			meta.AlsoTrust = append(meta.AlsoTrust, strings.ToLower(also.XMLName.Local))
		}
	}
	if trusted != nil {
		for _, trust := range trusted.Trust {
			rule := TrustRule{
				Group:   trust.Group,
				Name:    trust.Name,
				Version: trust.Version,
				File:    trust.File,
				Regex:   trust.Regex,
			}
			if trust.AlsoTrust != nil {
				for _, also := range trust.AlsoTrust.Checksums {
					alg := strings.ToLower(also.XMLName.Local)
					rule.AlsoTrustAlgs = append(rule.AlsoTrustAlgs, alg)
					meta.AlsoTrust = append(meta.AlsoTrust, alg)
				}
			}
			meta.TrustRules = append(meta.TrustRules, rule)
		}
	}

	return meta, nil
}

// ParseMetadataBytes is a convenience wrapper around ParseMetadata.
func ParseMetadataBytes(data []byte) (*Metadata, error) {
	return ParseMetadata(bytes.NewReader(data))
}

func parseChecksums(entries []xmlChecksum, parseErrors *[]string) []Checksum {
	var checksums []Checksum
	for _, entry := range entries {
		local := entry.XMLName.Local
		if local == "also-trust" {
			continue
		}
		alg := strings.ToLower(local)
		if !primaryChecksums[alg] {
			*parseErrors = append(*parseErrors, fmt.Sprintf(
				"unknown checksum element <%s>; algorithm is not trusted by default", local))
		}
		checksums = append(checksums, Checksum{
			Algorithm: alg,
			Value:     entry.Value,
			AlsoTrust: false,
		})
	}
	return checksums
}

// ParseLockfile reads a Gradle lockfile in properties format:
//
//	group:name:version=configs
//	group:name:version:classifier:extension=configs
//
// Blank lines and comment lines (starting with '#') are ignored.
func ParseLockfile(r io.Reader) (*Lockfile, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	lock := &Lockfile{}
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key := line
		if idx := strings.IndexByte(line, '='); idx >= 0 {
			key = line[:idx]
		}
		parts := strings.Split(key, ":")
		if len(parts) < 3 {
			return nil, fmt.Errorf("lockfile line %d: malformed coordinate %q", lineNumber+1, line)
		}
		entry := LockEntry{
			Group:   parts[0],
			Name:    parts[1],
			Version: parts[2],
		}
		if len(parts) >= 5 {
			entry.Classifier = parts[3]
			entry.Extension = parts[4]
		}
		lock.Entries = append(lock.Entries, entry)
	}
	return lock, nil
}

// ParseLockfileBytes is a convenience wrapper around ParseLockfile.
func ParseLockfileBytes(data []byte) (*Lockfile, error) {
	return ParseLockfile(bytes.NewReader(data))
}
