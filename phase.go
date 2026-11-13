package netsec

import (
	"fmt"
	"strings"
)

// Phase identifies which TLS 1.3 exporter secret is being used.
//
// The early exporter and the post-handshake exporter are derived from
// different parts of the TLS 1.3 key schedule. They are not interchangeable.
// The zero value is deliberately invalid so that a phase must be stated
// explicitly at the API boundary.
type Phase uint8

const (
	PhaseInvalid Phase = iota
	PhaseEarly
	PhasePostHandshake
)

const (
	// Aliases for callers that prefer the wording used in their stack.
	PhaseEarlyExporter         = PhaseEarly
	PhasePostHandshakeExporter = PhasePostHandshake
	EarlyExporter              = PhaseEarly
	PostHandshakeExporter      = PhasePostHandshake
	Phase0RTT                  = PhaseEarly
	Early                      = PhaseEarly
	EarlyExport                = PhaseEarly
	EarlyExportPhase           = PhaseEarly
	EarlyPhase                 = PhaseEarly
	PhaseHandshakeExporter     = PhasePostHandshake
	PhaseMain                  = PhasePostHandshake
	PhaseExporter              = PhasePostHandshake
	ExporterPhaseEarly         = PhaseEarly
	ExporterPhasePostHandshake = PhasePostHandshake
	EarlyExporterPhase         = PhaseEarly
	PostHandshakeExporterPhase = PhasePostHandshake
	PostHandshake              = PhasePostHandshake
	PostHandshakeExportPhase   = PhasePostHandshake
	PostHandshakePhase         = PhasePostHandshake
	Handshake                  = PhasePostHandshake
	Main                       = PhasePostHandshake
)

// Phase type aliases used by some TLS stacks.
type (
	ExporterPhase = Phase
	ExportPhase   = Phase
)

// Valid reports whether p names a TLS 1.3 exporter phase.
func (p Phase) Valid() bool {
	return p == PhaseEarly || p == PhasePostHandshake
}

// IsEarly reports whether p is the early (0-RTT) exporter phase.
func (p Phase) IsEarly() bool {
	return p == PhaseEarly
}

// IsPostHandshake reports whether p is the post-handshake exporter phase.
func (p Phase) IsPostHandshake() bool {
	return p == PhasePostHandshake
}

func (p Phase) String() string {
	switch p {
	case PhaseEarly:
		return "early"
	case PhasePostHandshake:
		return "post-handshake"
	default:
		return "invalid"
	}
}

// ParsePhase accepts the common spellings used for the two exporter phases.
func ParsePhase(s string) (Phase, error) {
	n := strings.ToLower(strings.TrimSpace(s))
	n = strings.ReplaceAll(n, "_", "")
	n = strings.ReplaceAll(n, "-", "")
	n = strings.ReplaceAll(n, " ", "")
	switch n {
	case "early", "earlyexport", "earlyexporter", "earlyexporterkey",
		"eexpmaster", "0rtt", "prehandshake":
		return PhaseEarly, nil
	case "posthandshake", "posthandshakeexport", "posthandshakeexporter",
		"handshake", "handshakeexport", "main", "mainexport", "exporter",
		"exportermaster", "exportermastersecret", "regular":
		return PhasePostHandshake, nil
	default:
		return PhaseInvalid, fmt.Errorf("%w: %q", ErrInvalidPhase, s)
	}
}

// PhaseFromString is an alias for ParsePhase.
func PhaseFromString(s string) (Phase, error) {
	return ParsePhase(s)
}

// ParseExporterPhase is an alias for ParsePhase.
func ParseExporterPhase(s string) (Phase, error) {
	return ParsePhase(s)
}

func phaseFromValue(v any) (Phase, bool) {
	switch p := v.(type) {
	case Phase:
		if p.Valid() {
			return p, true
		}
		return PhaseInvalid, false
	case string:
		phase, err := ParsePhase(p)
		return phase, err == nil
	case int:
		switch p {
		case 0:
			return PhaseEarly, true
		case 1:
			return PhasePostHandshake, true
		}
	case uint8:
		switch p {
		case 0:
			return PhaseEarly, true
		case 1:
			return PhasePostHandshake, true
		}
	}
	return PhaseInvalid, false
}

// PhaseOf reports the exporter phase associated with a secret, exporter, or
// phase value. It does not guess the phase of an untyped raw []byte secret.
func PhaseOf(v any) (Phase, error) {
	switch s := v.(type) {
	case Phase:
		if s.Valid() {
			return s, nil
		}
	case string:
		return ParsePhase(s)
	case *Exporter:
		if s == nil || !s.phase.Valid() {
			return PhaseInvalid, ErrInvalidPhase
		}
		return s.phase, nil
	case *EarlyExporterMasterSecret:
		if s == nil {
			return PhaseInvalid, ErrInvalidSecret
		}
		return PhaseEarly, nil
	case *ExporterMasterSecret:
		if s == nil {
			return PhaseInvalid, ErrInvalidSecret
		}
		return PhasePostHandshake, nil
	case *EarlySecret:
		if s == nil {
			return PhaseInvalid, ErrInvalidSecret
		}
		return PhaseEarly, nil
	case *MasterSecret:
		if s == nil {
			return PhaseInvalid, ErrInvalidSecret
		}
		return PhasePostHandshake, nil
	}
	return PhaseInvalid, ErrInvalidPhase
}

// SecretPhase is an alias for PhaseOf.
func SecretPhase(v any) (Phase, error) {
	return PhaseOf(v)
}
