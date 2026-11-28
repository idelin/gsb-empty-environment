package dmarc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Policy is a DMARC disposition policy.
type Policy string

const (
	PolicyNone       Policy = "none"
	PolicyQuarantine Policy = "quarantine"
	PolicyReject     Policy = "reject"
)

// Alignment is the DMARC identifier alignment mode.
type Alignment string

const (
	AlignmentRelaxed Alignment = "r"
	AlignmentStrict  Alignment = "s"
)

// Record is a parsed DMARC policy record.
type Record struct {
	Raw             string    `json:"raw"`
	Version         string    `json:"version"`
	Policy          Policy    `json:"p"`
	SubdomainPolicy Policy    `json:"sp,omitempty"`
	ADKIM           Alignment `json:"adkim"`
	ASPF            Alignment `json:"aspf"`
	Pct             int       `json:"pct"`
	RUA             []string  `json:"rua,omitempty"`
	RUF             []string  `json:"ruf,omitempty"`
	FO              string    `json:"fo,omitempty"`
	RF              string    `json:"rf,omitempty"`
}

// ParseRecord parses one DMARC TXT record.
func ParseRecord(raw string) (*Record, error) {
	record := &Record{
		Raw:     strings.TrimSpace(raw),
		ADKIM:   AlignmentRelaxed,
		ASPF:    AlignmentRelaxed,
		Pct:     100,
		Version: "",
	}
	tags := make(map[string]string)
	for _, segment := range strings.Split(record.Raw, ";") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		name, value, ok := strings.Cut(segment, "=")
		if !ok {
			return nil, errors.New("dmarc: malformed tag")
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name == "" {
			return nil, errors.New("dmarc: empty tag name")
		}
		if _, exists := tags[name]; exists {
			return nil, fmt.Errorf("dmarc: duplicate tag %s", name)
		}
		tags[name] = value
	}
	version := tags["v"]
	if !strings.EqualFold(version, "DMARC1") {
		return nil, errors.New("dmarc: record does not start with v=DMARC1")
	}
	record.Version = "DMARC1"
	policy, err := parsePolicy(tags["p"])
	if err != nil {
		return nil, err
	}
	record.Policy = policy
	if value := tags["sp"]; value != "" {
		subdomainPolicy, err := parsePolicy(value)
		if err != nil {
			return nil, fmt.Errorf("dmarc: invalid sp: %w", err)
		}
		record.SubdomainPolicy = subdomainPolicy
	}
	if value := strings.ToLower(tags["adkim"]); value != "" {
		alignment, err := parseAlignment(value)
		if err != nil {
			return nil, fmt.Errorf("dmarc: invalid adkim: %w", err)
		}
		record.ADKIM = alignment
	}
	if value := strings.ToLower(tags["aspf"]); value != "" {
		alignment, err := parseAlignment(value)
		if err != nil {
			return nil, fmt.Errorf("dmarc: invalid aspf: %w", err)
		}
		record.ASPF = alignment
	}
	if value := tags["pct"]; value != "" {
		pct, err := strconv.Atoi(value)
		if err != nil || pct < 0 || pct > 100 {
			return nil, errors.New("dmarc: invalid pct")
		}
		record.Pct = pct
	}
	record.RUA = splitURIs(tags["rua"])
	record.RUF = splitURIs(tags["ruf"])
	record.FO = tags["fo"]
	record.RF = tags["rf"]
	return record, nil
}

// Parse is an alias for ParseRecord.
func Parse(raw string) (*Record, error) { return ParseRecord(raw) }

func parsePolicy(value string) (Policy, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return PolicyNone, nil
	case "quarantine":
		return PolicyQuarantine, nil
	case "reject":
		return PolicyReject, nil
	default:
		return "", errors.New("dmarc: invalid or missing p policy")
	}
}

func parseAlignment(value string) (Alignment, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "r", "relaxed":
		return AlignmentRelaxed, nil
	case "s", "strict":
		return AlignmentStrict, nil
	default:
		return "", errors.New("invalid alignment mode")
	}
}

func splitURIs(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
