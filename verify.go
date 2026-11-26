package pathsec

import "time"

// Config controls joint validation strictness.
type Config struct {
	// RequireROA makes absent/NotFound ROA material affect admission. The
	// technical ROA status remains NotFound either way.
	RequireROA bool
	// RequireASPAUnknown is accepted only when false; by default Unknown is
	// not promoted to Valid.
	RequireASPA bool
	// RequireBGPsec rejects requests without a BGPsec proof as Unknown.
	RequireBGPsec bool
	// EnforcePCountMultiplicity compares pCount to adjacent AS run sizes.
	EnforcePCountMultiplicity bool
	// RequireFinalBGPsecTarget rejects a missing local target AS.
	RequireFinalBGPsecTarget bool
	// RoleIsLocal interprets VerifyRequest.Role as an RFC 9234 local role.
	RoleIsLocal bool
	// AllowUnattestedApex relaxes ASPA adjacent-apex handling.
	AllowUnattestedApex bool
}

// StrictConfig returns a configuration requiring all three security systems.
func StrictConfig() Config {
	return Config{
		RequireROA:                true,
		RequireASPA:               true,
		RequireBGPsec:             true,
		EnforcePCountMultiplicity: true,
		RequireFinalBGPsecTarget:  true,
	}
}

// VerifyRequest is the joint validation entry.
type VerifyRequest struct {
	Now time.Time `json:"now,omitempty"`

	// Prefix is the target IPv4/IPv6 prefix. PrefixText, TargetPrefix and
	// IPPrefix are accepted aliases for host JSON fixtures.
	Prefix       string `json:"prefix,omitempty"`
	PrefixText   string `json:"prefixText,omitempty"`
	TargetPrefix string `json:"targetPrefix,omitempty"`
	IPPrefix     string `json:"ipPrefix,omitempty"`

	// OriginASN defaults to the rightmost AS_PATH element.
	OriginASN uint32 `json:"originASN,omitempty"`
	OriginAS  uint32 `json:"originAS,omitempty"`

	// ASPath is conventional BGP order: nearest AS first, origin last.
	ASPath  []uint32 `json:"asPath,omitempty"`
	Path    []uint32 `json:"path,omitempty"`
	ASNs    []uint32 `json:"asns,omitempty"`
	AS_PATH []uint32 `json:"AS_PATH,omitempty"`

	Segments []ASPathSegment `json:"segments,omitempty"`
	HasASSet bool            `json:"hasASSet,omitempty"`
	ASSet    []uint32        `json:"asSet,omitempty"`

	Role        Role   `json:"role,omitempty"`
	LocalRole   Role   `json:"localRole,omitempty"`
	ReceiveSide Role   `json:"receiveSide,omitempty"`
	Perspective Role   `json:"perspective,omitempty"`
	LocalASN    uint32 `json:"localASN,omitempty"`
	PeerASN     uint32 `json:"peerASN,omitempty"`

	KnownPeerEdges [][2]uint32 `json:"knownPeerEdges,omitempty"`

	BGPsec          *BGPsecProof    `json:"bgpsec,omitempty"`
	SecurePath      []SecurePathHop `json:"securePath,omitempty"`
	SignatureBlock  []Signature     `json:"signatureBlock,omitempty"`
	Secure_Path     []SecurePathHop `json:"Secure_Path,omitempty"`
	Signature_Block []Signature     `json:"Signature_Block,omitempty"`

	// ConfigOverride, when non-nil, replaces Validator.Config for this call.
	ConfigOverride *Config `json:"-"`
}

// Result is the joint validation result.
type Result struct {
	Status         Status       `json:"status"`
	Overall        Status       `json:"overall,omitempty"`
	ROAStatus      Status       `json:"roaStatus,omitempty"`
	ASPAStatus     Status       `json:"aspaStatus,omitempty"`
	BGPsecStatus   Status       `json:"bgpsecStatus,omitempty"`
	Valid          bool         `json:"valid"`
	Prefix         string       `json:"prefix"`
	OriginASN      uint32       `json:"originASN"`
	NormalizedPath []uint32     `json:"normalizedPath"`
	ROA            ROAResult    `json:"roa"`
	ASPA           ASPAResult   `json:"aspa"`
	BGPsec         BGPsecResult `json:"bgpsec"`
	Reasons        []Reason     `json:"reasons,omitempty"`
}

// Validator bundles ROA, ASPA and trusted BGPsec key tables.
type Validator struct {
	ROA    *ROATable
	ASPA   *ASPATable
	Keys   *KeyStore
	Config Config
}

// NewValidator creates an empty joint validator.
func NewValidator() *Validator {
	return &Validator{
		ROA:  NewROATable(),
		ASPA: NewASPATable(),
		Keys: NewKeyStore(),
	}
}

// NewStrictValidator returns a validator requiring ROA coverage, ASPA and a
// complete BGPsec proof.
func NewStrictValidator() *Validator {
	v := NewValidator()
	v.Config = StrictConfig()
	return v
}

// NewJointValidator creates a validator from existing stores.
func NewJointValidator(roa *ROATable, aspa *ASPATable, keys *KeyStore) *Validator {
	if roa == nil {
		roa = NewROATable()
	}
	if aspa == nil {
		aspa = NewASPATable()
	}
	if keys == nil {
		keys = NewKeyStore()
	}
	return &Validator{ROA: roa, ASPA: aspa, Keys: keys}
}

// Verify runs ROA, ASPA and BGPsec checks and aggregates their statuses.
func (v *Validator) Verify(req VerifyRequest) Result {
	cfg := Config{}
	if v != nil {
		cfg = v.Config
	}
	if req.ConfigOverride != nil {
		cfg = *req.ConfigOverride
	}

	prefixText := firstNonEmpty(req.Prefix, req.PrefixText, req.TargetPrefix, req.IPPrefix)
	path := firstNonNilSlice(req.ASPath, req.AS_PATH, req.Path, req.ASNs)
	origin := firstNonZero(req.OriginASN, req.OriginAS)

	aspaReq := ASPARequest{
		ASPath:              path,
		Segments:            req.Segments,
		HasASSet:            req.HasASSet,
		ASSet:               req.ASSet,
		Role:                req.Role,
		LocalRole:           req.LocalRole,
		ReceiveSide:         req.ReceiveSide,
		Perspective:         req.Perspective,
		RoleIsLocal:         cfg.RoleIsLocal,
		LocalASN:            req.LocalASN,
		KnownPeerEdges:      req.KnownPeerEdges,
		AllowUnattestedApex: cfg.AllowUnattestedApex,
	}
	aspaTable := (*ASPATable)(nil)
	if v != nil {
		aspaTable = v.ASPA
	}
	aspaResult := VerifyASPA(aspaTable, aspaReq)
	normalizedPath := aspaResult.Path
	if origin == 0 && len(normalizedPath) > 0 {
		origin = normalizedPath[len(normalizedPath)-1]
	}

	result := Result{
		Status:         StatusValid,
		Prefix:         prefixText,
		OriginASN:      origin,
		NormalizedPath: normalizedPath,
		ASPA:           aspaResult,
	}

	roaTable := (*ROATable)(nil)
	if v != nil {
		roaTable = v.ROA
	}
	if prefixText != "" {
		result.ROA = CheckROA(roaTable, prefixText, origin, req.Now)
		result.Prefix = result.ROA.Prefix
		result.Status = statusWorse(result.Status, normalizeJointStatus(result.ROA.Status, cfg.RequireROA))
		result.Reasons = append(result.Reasons, result.ROA.Reasons...)
	} else {
		result.ROA = ROAResult{Status: StatusSkipped}
	}

	result.Status = statusWorse(result.Status, aspaResult.Status)
	for _, reason := range aspaResult.Reasons {
		result.Reasons = append(result.Reasons, reason)
	}

	securePathHops := firstNonNilHopSlice(req.SecurePath, req.Secure_Path)
	signatureEntries := firstNonNilSignatureSlice(req.SignatureBlock, req.Signature_Block)
	hasBGPsec := req.BGPsec != nil || len(securePathHops) > 0 || len(signatureEntries) > 0
	if hasBGPsec {
		bgpReq := BGPsecRequest{
			Prefix:             prefixText,
			ASPath:             normalizedPath,
			Normalized:         aspaResult.Normalized,
			Proof:              req.BGPsec,
			SecurePath:         securePathHops,
			SignatureBlock:     signatureEntries,
			TargetASN:          req.LocalASN,
			LocalASN:           req.LocalASN,
			Now:                req.Now,
			EnforceRunCounts:   cfg.EnforcePCountMultiplicity,
			RequireFinalTarget: cfg.RequireFinalBGPsecTarget,
		}
		keys := (*KeyStore)(nil)
		if v != nil {
			keys = v.Keys
		}
		result.BGPsec = VerifyBGPsec(keys, bgpReq)
		result.Status = statusWorse(result.Status, result.BGPsec.Status)
		result.Reasons = append(result.Reasons, result.BGPsec.Reasons...)
	} else {
		result.BGPsec = BGPsecResult{Status: StatusSkipped}
		if cfg.RequireBGPsec {
			result.Status = statusWorse(result.Status, StatusUnknown)
			result.Reasons = append(result.Reasons, Reason{Code: "bgpsec_required", Message: "BGPsec proof is required but absent"})
		}
	}

	if cfg.RequireROA && result.ROA.Status == StatusNotFound {
		result.Status = statusWorse(result.Status, StatusUnknown)
	}
	result.Valid = result.Status == StatusValid
	result.Overall = result.Status
	result.ROAStatus = result.ROA.Status
	result.ASPAStatus = result.ASPA.Status
	result.BGPsecStatus = result.BGPsec.Status
	return result
}

// Verify is a package-level convenience entry point. Nil stores are allowed.
func Verify(req VerifyRequest, roa *ROATable, aspa *ASPATable, keys *KeyStore) Result {
	return NewJointValidator(roa, aspa, keys).Verify(req)
}

func normalizeJointStatus(status Status, required bool) Status {
	if status == StatusNotFound {
		if required {
			return StatusUnknown
		}
		return StatusUnknown
	}
	return status
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonNilSlice(values ...[]uint32) []uint32 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonNilHopSlice(values ...[]SecurePathHop) []SecurePathHop {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonNilSignatureSlice(values ...[]Signature) []Signature {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
