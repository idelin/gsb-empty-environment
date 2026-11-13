package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// ClaimName is a canonical workload claim understood by the mapping.
type ClaimName string

const (
	ClaimSub            ClaimName = "sub"
	ClaimNamespace      ClaimName = "namespace"
	ClaimServiceAccount ClaimName = "serviceAccount"
	ClaimRepository     ClaimName = "repository"
	ClaimRef            ClaimName = "ref"
)

// DefaultMaxPrincipalBytes is used when MaxPrincipalBytes is zero.
const DefaultMaxPrincipalBytes = 1024

// DefaultAuditBudget is used when AuditBudget is zero.
const DefaultAuditBudget = 1_000_000

var canonicalClaimOrder = []ClaimName{
	ClaimSub,
	ClaimNamespace,
	ClaimServiceAccount,
	ClaimRepository,
	ClaimRef,
}

// SupportedClaims returns the canonical claims supported by claim patterns.
func SupportedClaims() []ClaimName {
	return append([]ClaimName(nil), canonicalClaimOrder...)
}

func supportedClaim(name string) (ClaimName, bool) {
	switch ClaimName(name) {
	case ClaimSub:
		return ClaimSub, true
	case ClaimNamespace:
		return ClaimNamespace, true
	case ClaimServiceAccount:
		return ClaimServiceAccount, true
	case ClaimRepository:
		return ClaimRepository, true
	case ClaimRef:
		return ClaimRef, true
	default:
		return "", false
	}
}

// ClaimPatterns maps canonical claims to their source claim names. An empty
// field means the canonical name itself. Source names are compared exactly and
// are never lowercased, trimmed, or path-normalized.
type ClaimPatterns struct {
	Sub            string
	Namespace      string
	ServiceAccount string
	Repository     string
	Ref            string
}

// DefaultClaimPatterns returns identity mappings for all supported claims.
func DefaultClaimPatterns() ClaimPatterns {
	return ClaimPatterns{
		Sub:            string(ClaimSub),
		Namespace:      string(ClaimNamespace),
		ServiceAccount: string(ClaimServiceAccount),
		Repository:     string(ClaimRepository),
		Ref:            string(ClaimRef),
	}
}

// SourceFor returns the source claim name for a canonical claim.
func (p ClaimPatterns) SourceFor(claim ClaimName) (string, bool) {
	if _, ok := supportedClaim(string(claim)); !ok {
		return "", false
	}
	var source string
	switch claim {
	case ClaimSub:
		source = p.Sub
	case ClaimNamespace:
		source = p.Namespace
	case ClaimServiceAccount:
		source = p.ServiceAccount
	case ClaimRepository:
		source = p.Repository
	case ClaimRef:
		source = p.Ref
	}
	if source == "" {
		source = string(claim)
	}
	return source, source != ""
}

// ProviderConfig defines one immutable identity provider generation.
//
// Issuer is matched byte-for-byte. Audiences is the allowed audience set.
// ClaimPatterns selects the source fields for canonical claims. Template
// contains literal bytes and {claim} placeholders. Languages supplies the
// finite value language used by collision auditing for every listed claim.
type ProviderConfig struct {
	ProviderID        string
	Issuer            string
	Audiences         []string
	ClaimPatterns     ClaimPatterns
	Template          string
	Languages         map[ClaimName]AllowedLanguage
	MaxPrincipalBytes int
	AuditBudget       int
	Generation        uint64
}

// Validate checks a configuration but does not run collision auditing.
func (c ProviderConfig) Validate() error {
	_, err := prepareConfig(c)
	return err
}

type configSnapshot struct {
	providerID        string
	issuer            string
	audiences         []string
	audienceSet       map[string]struct{}
	claimPatterns     ClaimPatterns
	template          *parsedTemplate
	languages         map[ClaimName]*language
	claimsInAudit     []ClaimName
	maxPrincipalBytes int
	auditBudget       int
}

func prepareConfig(config ProviderConfig) (*configSnapshot, error) {
	if config.ProviderID == "" {
		return nil, fmt.Errorf("%w: ProviderID is empty", ErrInvalidConfig)
	}
	if config.Issuer == "" {
		return nil, fmt.Errorf("%w: Issuer is empty", ErrInvalidConfig)
	}

	audiences := make([]string, 0, len(config.Audiences))
	audienceSet := make(map[string]struct{}, len(config.Audiences))
	for _, audience := range config.Audiences {
		if audience == "" {
			return nil, fmt.Errorf("%w: audience is empty", ErrInvalidConfig)
		}
		if _, exists := audienceSet[audience]; exists {
			continue
		}
		audienceSet[audience] = struct{}{}
		audiences = append(audiences, audience)
	}
	if len(audiences) == 0 {
		return nil, fmt.Errorf("%w: Audiences is empty", ErrInvalidConfig)
	}

	patterns := config.ClaimPatterns
	if patterns.Sub == "" {
		patterns.Sub = string(ClaimSub)
	}
	if patterns.Namespace == "" {
		patterns.Namespace = string(ClaimNamespace)
	}
	if patterns.ServiceAccount == "" {
		patterns.ServiceAccount = string(ClaimServiceAccount)
	}
	if patterns.Repository == "" {
		patterns.Repository = string(ClaimRepository)
	}
	if patterns.Ref == "" {
		patterns.Ref = string(ClaimRef)
	}

	parsed, err := parseTemplate(config.Template)
	if err != nil {
		return nil, err
	}
	if parsed.placeholders == 0 {
		return nil, fmt.Errorf("%w: Template must contain at least one claim placeholder", ErrInvalidConfig)
	}

	languages := make(map[ClaimName]*language, len(config.Languages))
	for claim, definition := range config.Languages {
		if _, ok := supportedClaim(string(claim)); !ok {
			return nil, fmt.Errorf("%w: unsupported claim %q", ErrInvalidConfig, claim)
		}
		compiled, err := compileLanguage(claim, definition)
		if err != nil {
			return nil, err
		}
		languages[claim] = compiled
	}
	for _, claim := range parsed.distinctClaims {
		if _, ok := languages[claim]; !ok {
			return nil, fmt.Errorf("%w: no allowed language for template claim %q", ErrInvalidConfig, claim)
		}
	}

	claimsInAudit := make([]ClaimName, 0, len(languages))
	for _, claim := range canonicalClaimOrder {
		if _, ok := languages[claim]; ok {
			claimsInAudit = append(claimsInAudit, claim)
		}
	}

	maxPrincipalBytes := config.MaxPrincipalBytes
	if maxPrincipalBytes == 0 {
		maxPrincipalBytes = DefaultMaxPrincipalBytes
	}
	if maxPrincipalBytes < 0 {
		return nil, fmt.Errorf("%w: MaxPrincipalBytes must be positive", ErrInvalidConfig)
	}

	auditBudget := config.AuditBudget
	if auditBudget == 0 {
		auditBudget = DefaultAuditBudget
	}
	if auditBudget < 0 {
		return nil, fmt.Errorf("%w: AuditBudget must be positive", ErrInvalidConfig)
	}

	if len(parsed.literalBytes()) > maxPrincipalBytes {
		return nil, fmt.Errorf("%w: template literals exceed MaxPrincipalBytes", ErrPrincipalTooLong)
	}

	return &configSnapshot{
		providerID:        config.ProviderID,
		issuer:            config.Issuer,
		audiences:         audiences,
		audienceSet:       audienceSet,
		claimPatterns:     patterns,
		template:          parsed,
		languages:         languages,
		claimsInAudit:     claimsInAudit,
		maxPrincipalBytes: maxPrincipalBytes,
		auditBudget:       auditBudget,
	}, nil
}

func (s *configSnapshot) digest(generation uint64) [32]byte {
	hash := sha256.New()
	writeHashString(hash, s.providerID)
	writeHashString(hash, s.issuer)
	writeHashUint64(hash, generation)
	writeHashUint64(hash, uint64(len(s.audiences)))
	for _, audience := range s.audiences {
		writeHashString(hash, audience)
	}
	writeHashString(hash, s.claimPatterns.Sub)
	writeHashString(hash, s.claimPatterns.Namespace)
	writeHashString(hash, s.claimPatterns.ServiceAccount)
	writeHashString(hash, s.claimPatterns.Repository)
	writeHashString(hash, s.claimPatterns.Ref)
	writeHashBytes(hash, s.template.rawBytes())
	writeHashUint64(hash, uint64(s.maxPrincipalBytes))
	writeHashUint64(hash, uint64(len(s.languages)))
	for _, claim := range canonicalClaimOrder {
		lang, ok := s.languages[claim]
		if !ok {
			continue
		}
		writeHashString(hash, string(claim))
		if lang.words != nil {
			writeHashUint64(hash, 1)
			writeHashUint64(hash, uint64(len(lang.words)))
			for _, word := range lang.words {
				writeHashString(hash, word)
			}
		} else {
			writeHashUint64(hash, 2)
			writeHashString(hash, lang.prefix)
			writeHashUint64(hash, uint64(len(lang.suffixes)))
			for _, suffix := range lang.suffixes {
				writeHashString(hash, suffix)
			}
		}
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashString(writer hashWriter, value string) {
	writeHashBytes(writer, []byte(value))
}

func writeHashBytes(writer hashWriter, value []byte) {
	writeHashUint64(writer, uint64(len(value)))
	_, _ = writer.Write(value)
}

func writeHashUint64(writer hashWriter, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func compareTuples(left, right ClaimTuple) int {
	for _, claim := range canonicalClaimOrder {
		lv, lok := left[claim]
		rv, rok := right[claim]
		if !lok && !rok {
			continue
		}
		if !lok {
			return -1
		}
		if !rok {
			return 1
		}
		if comparison := bytes.Compare([]byte(lv), []byte(rv)); comparison != 0 {
			return comparison
		}
	}
	return 0
}
