package netsec

import (
	"fmt"
	"strings"
)

// Policy contains the caller supplied admission configuration used by an ante
// handler.
type Policy struct {
	// AminoRegistry resolves protobuf message type URLs to Amino names. It may
	// be an AminoRegistry, map[string]string, map[string]bool, map[string]struct{},
	// or []string. A nil registry means no legacy Amino messages are allowed.
	AminoRegistry any

	// LegacyAminoJSONDeprecationHeight activates the signer allowlist gate once
	// the block height reaches this value. Zero disables the height gate.
	LegacyAminoJSONDeprecationHeight uint64

	// LegacyAminoJSONWhitelist contains the only addresses allowed to use
	// SIGN_MODE_LEGACY_AMINO_JSON at or after the deprecation height.
	LegacyAminoJSONWhitelist []string
}

// AdmissionPolicy is an alias for Policy.
type AdmissionPolicy = Policy

// Config is an alias for Policy.
type Config = Policy

// AnteConfig is an alias for Policy.
type AnteConfig = Policy

// NewPolicy creates a policy with the supplied Amino registry and legacy
// Amino JSON height gate.
func NewPolicy(registry any, deprecationHeight uint64, whitelist []string) Policy {
	return Policy{
		AminoRegistry:                    registry,
		LegacyAminoJSONDeprecationHeight: deprecationHeight,
		LegacyAminoJSONWhitelist:         append([]string(nil), whitelist...),
	}
}

// Request is the ante context supplied with a transaction.
type Request struct {
	Height  uint64
	Signers []string

	// SignMode optionally overrides the modes decoded from AuthInfo. Ante
	// handlers that already resolved the sign mode can set this directly.
	SignMode *SignMode
}

// AdmissionRequest is an alias for Request.
type AdmissionRequest = Request

// Validate checks a decoded transaction against the admission rules. The
// checks are:
//
//  1. extension_options must be empty for every sign mode;
//  2. unknown fields below 1024 are critical and always rejected;
//  3. unknown fields at or above 1024 are non-critical and may only be
//     ignored for SIGN_MODE_DIRECT;
//  4. legacy Amino JSON may only reference messages with a registered Amino
//     name;
//  5. at or after the configured height, legacy Amino JSON is only allowed
//     for allowlisted signer addresses.
func (p Policy) Validate(tx *Tx, request Request) error {
	return p.ValidateTx(tx, request)
}

// ValidateTx checks a decoded transaction against the admission rules.
func (p Policy) ValidateTx(tx *Tx, request Request) error {
	if tx == nil {
		return ErrMalformedTx
	}
	if tx.Body == nil {
		return fmt.Errorf("%w: transaction body is missing", ErrMalformedTx)
	}

	if err := ValidateExtensionOptions(tx.Body.ExtensionOptions); err != nil {
		return err
	}

	modes := p.effectiveSignModes(tx, request)
	if err := validateUnknownFieldsForModes(tx.AllUnknownFields(), modes); err != nil {
		return err
	}

	if len(tx.Body.NonCriticalExtensionOptions) > 0 && !allDirectModes(modes) {
		return fmt.Errorf(
			"%w: %d non_critical_extension_options present",
			ErrNonCriticalExtensionOptions,
			len(tx.Body.NonCriticalExtensionOptions),
		)
	}

	if containsMode(modes, SignModeLegacyAminoJSON) {
		if err := ValidateAminoMessages(tx.Body.Messages, p.AminoRegistry); err != nil {
			return err
		}
		signers := request.Signers
		if len(signers) == 0 {
			signers = tx.Signers
		}
		height := request.Height
		if height == 0 {
			height = tx.Height
		}
		if err := ValidateAminoDeprecation(
			height,
			modes,
			signers,
			p.LegacyAminoJSONDeprecationHeight,
			p.LegacyAminoJSONWhitelist,
		); err != nil {
			return err
		}
	}
	return nil
}

// AnteHandle is the ante-facing entry point for an already decoded tx.
func (p Policy) AnteHandle(tx *Tx, request Request) error {
	return p.ValidateTx(tx, request)
}

// AnteHandler is a function suitable for an ante processing pipeline.
type AnteHandler func(tx *Tx, request Request) error

// Handler returns the policy as an AnteHandler function.
func (p Policy) Handler() AnteHandler {
	return p.AnteHandle
}

// Check validates a decoded transaction using context carried on the Tx.
func (p Policy) Check(tx *Tx) error {
	return p.ValidateTx(tx, Request{})
}

// CheckRaw decodes and validates raw transaction bytes using context carried
// on the decoded Tx.
func (p Policy) CheckRaw(raw []byte) error {
	_, err := p.ValidateRawTx(raw, Request{})
	return err
}

// ValidateRawTx decodes raw transaction bytes and applies admission checks.
func (p Policy) ValidateRawTx(raw []byte, request Request) (*Tx, error) {
	tx, err := ParseTx(raw)
	if err != nil {
		return nil, err
	}
	tx.Height = request.Height
	tx.Signers = append([]string(nil), request.Signers...)
	if err := p.ValidateTx(tx, request); err != nil {
		return nil, err
	}
	return tx, nil
}

// Decode decodes and validates raw transaction bytes.
func (p Policy) Decode(raw []byte, request Request) (*Tx, error) {
	return p.ValidateRawTx(raw, request)
}

// ValidateTx is the package level form of Policy.ValidateTx.
func ValidateTx(policy Policy, tx *Tx, request Request) error {
	return policy.ValidateTx(tx, request)
}

// CheckTx decodes raw transaction bytes and applies admission checks.
func CheckTx(raw []byte, policy Policy, request Request) error {
	_, err := policy.ValidateRawTx(raw, request)
	return err
}

func (p Policy) effectiveSignModes(tx *Tx, request Request) []SignMode {
	if request.SignMode != nil {
		return []SignMode{*request.SignMode}
	}
	return tx.SignModes()
}

func allDirectModes(modes []SignMode) bool {
	if len(modes) == 0 {
		return false
	}
	for _, mode := range modes {
		if mode != SignModeDirect {
			return false
		}
	}
	return true
}

func containsMode(modes []SignMode, want SignMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}

func validateUnknownFieldsForModes(fields []UnknownField, modes []SignMode) error {
	for _, field := range fields {
		if field.Critical || IsCriticalUnknownField(field.Number) {
			return fmt.Errorf(
				"%w: field %d in %s at %s",
				ErrCriticalUnknownField,
				field.Number,
				field.Scope,
				field.Path,
			)
		}
		if !allDirectModes(modes) {
			return fmt.Errorf(
				"%w: field %d in %s at %s",
				ErrNonCriticalUnknownField,
				field.Number,
				field.Scope,
				field.Path,
			)
		}
	}
	return nil
}

// ValidateUnknownFields applies the unknown-field policy for one sign mode.
func ValidateUnknownFields(fields []UnknownField, mode SignMode) error {
	return validateUnknownFieldsForModes(fields, []SignMode{mode})
}

// ValidateExtensionOptions rejects any non-empty extension_options list.
func ValidateExtensionOptions(options []*Any) error {
	if len(options) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %d entries", ErrExtensionOptions, len(options))
}

// ValidateAminoMessages rejects messages whose type URL has no Amino name in
// the caller registry. It is used for SIGN_MODE_LEGACY_AMINO_JSON.
func ValidateAminoMessages(messages []*Any, registry any) error {
	for i, message := range messages {
		if message == nil {
			return fmt.Errorf(
				"%w: message %d is nil",
				ErrAminoMessageNotRegistered,
				i,
			)
		}
		name, ok := LookupAminoName(registry, message.TypeURL)
		if !ok || strings.TrimSpace(name) == "" {
			return fmt.Errorf(
				"%w: message %d type_url %q",
				ErrAminoMessageNotRegistered,
				i,
				message.TypeURL,
			)
		}
	}
	return nil
}

// ValidateAminoDeprecation enforces the legacy Amino signer allowlist at and
// after the configured height. A zero deprecation height disables the gate.
func ValidateAminoDeprecation(
	height uint64,
	modes []SignMode,
	signers []string,
	deprecationHeight uint64,
	whitelist []string,
) error {
	if !containsMode(modes, SignModeLegacyAminoJSON) ||
		deprecationHeight == 0 ||
		height < deprecationHeight {
		return nil
	}
	if len(signers) == 0 {
		return fmt.Errorf(
			"%w: %w at height %d",
			ErrLegacyAminoJSONDeprecated,
			ErrNoLegacyAminoSigner,
			height,
		)
	}

	allowed := make(map[string]struct{}, len(whitelist))
	for _, address := range whitelist {
		address = strings.TrimSpace(address)
		if address != "" {
			allowed[address] = struct{}{}
		}
	}

	for _, signer := range signers {
		address := strings.TrimSpace(signer)
		if _, ok := allowed[address]; !ok {
			return fmt.Errorf(
				"%w: signer %q is not allowlisted at height %d",
				ErrLegacyAminoJSONDeprecated,
				address,
				height,
			)
		}
	}
	return nil
}

// Options and decoder convenience wrappers.

type decodeConfig struct {
	policy  Policy
	request Request
}

// DecodeOption configures DecodeTx.
type DecodeOption func(*decodeConfig)

// WithPolicy supplies the complete admission policy.
func WithPolicy(policy Policy) DecodeOption {
	return func(config *decodeConfig) {
		config.policy = policy
	}
}

// WithAminoRegistry supplies the caller Amino registry.
func WithAminoRegistry(registry any) DecodeOption {
	return func(config *decodeConfig) {
		config.policy.AminoRegistry = registry
	}
}

// WithLegacyAminoJSONDeprecationHeight sets the legacy Amino height gate.
func WithLegacyAminoJSONDeprecationHeight(height uint64) DecodeOption {
	return func(config *decodeConfig) {
		config.policy.LegacyAminoJSONDeprecationHeight = height
	}
}

// WithDeprecatedHeight is an alias for WithLegacyAminoJSONDeprecationHeight.
func WithDeprecatedHeight(height uint64) DecodeOption {
	return WithLegacyAminoJSONDeprecationHeight(height)
}

// WithAminoJSONWhitelist sets the legacy Amino signer allowlist.
func WithAminoJSONWhitelist(addresses ...string) DecodeOption {
	return func(config *decodeConfig) {
		config.policy.LegacyAminoJSONWhitelist = append([]string(nil), addresses...)
	}
}

// WithWhitelist is an alias for WithAminoJSONWhitelist.
func WithWhitelist(addresses ...string) DecodeOption {
	return WithAminoJSONWhitelist(addresses...)
}

// WithHeight sets the block height used by admission checks.
func WithHeight(height uint64) DecodeOption {
	return func(config *decodeConfig) {
		config.request.Height = height
	}
}

// WithSigners sets the signer addresses used by admission checks.
func WithSigners(addresses ...string) DecodeOption {
	return func(config *decodeConfig) {
		config.request.Signers = append([]string(nil), addresses...)
	}
}

// WithSignMode overrides the sign mode decoded from AuthInfo.
func WithSignMode(mode SignMode) DecodeOption {
	return func(config *decodeConfig) {
		config.request.SignMode = &mode
	}
}

// DecodeOptions is the struct form of DecodeTx options.
type DecodeOptions struct {
	AminoRegistry                    any
	LegacyAminoJSONDeprecationHeight uint64
	LegacyAminoJSONWhitelist         []string
	Height                           uint64
	Signers                          []string
	SignMode                         *SignMode
}

// DecodeTxWithOptions decodes and validates a transaction with struct options.
func DecodeTxWithOptions(raw []byte, options DecodeOptions) (*Tx, error) {
	policy := Policy{
		AminoRegistry:                    options.AminoRegistry,
		LegacyAminoJSONDeprecationHeight: options.LegacyAminoJSONDeprecationHeight,
		LegacyAminoJSONWhitelist:         options.LegacyAminoJSONWhitelist,
	}
	request := Request{
		Height:   options.Height,
		Signers:  options.Signers,
		SignMode: options.SignMode,
	}
	return policy.ValidateRawTx(raw, request)
}

// Decoder decodes and validates transactions with a fixed policy.
type Decoder struct {
	Policy Policy
}

// NewDecoder creates a transaction decoder with the supplied policy.
func NewDecoder(policy Policy) *Decoder {
	return &Decoder{Policy: policy}
}

// Decode decodes and validates one transaction.
func (d *Decoder) Decode(raw []byte, request Request) (*Tx, error) {
	if d == nil {
		return nil, ErrMalformedTx
	}
	return d.Policy.ValidateRawTx(raw, request)
}
