package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const defaultMaxManifestBytes = 16 << 20

// ParseAdapterManifest strictly decodes an unsigned manifest. Callers that
// accept remote adapters must verify the original bytes before using the
// result; Manager.Load performs that ordering itself.
func ParseAdapterManifest(payload []byte) (AdapterManifest, error) {
	return parseAdapterManifest(payload, defaultMaxManifestBytes)
}

func parseAdapterManifest(payload []byte, maxBytes int) (AdapterManifest, error) {
	var manifest AdapterManifest
	if len(payload) == 0 {
		return manifest, fmt.Errorf("%w: empty payload", ErrInvalidManifest)
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxManifestBytes
	}
	if len(payload) > maxBytes {
		return manifest, fmt.Errorf("%w: manifest exceeds size limit", ErrInvalidManifest)
	}
	if err := validateJSONDocument(payload); err != nil {
		return manifest, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}

	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return manifest, fmt.Errorf("%w: trailing JSON value", ErrInvalidManifest)
		}
		return manifest, fmt.Errorf("%w: trailing data: %v", ErrInvalidManifest, err)
	}
	return manifest, nil
}

func validateJSONDocument(payload []byte) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := validateJSONValue(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func validateJSONValue(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting is too deep")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}

	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			keys := make(map[string]struct{})
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				normalizedKey := strings.ToLower(key)
				if _, duplicate := keys[normalizedKey]; duplicate {
					return fmt.Errorf("duplicate object key %q", key)
				}
				keys[normalizedKey] = struct{}{}
				if err := validateJSONValue(dec, depth+1); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return fmt.Errorf("unterminated object")
			}
		case '[':
			for dec.More() {
				if err := validateJSONValue(dec, depth+1); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return fmt.Errorf("unterminated array")
			}
		default:
			return fmt.Errorf("unexpected delimiter %q", value)
		}
	case string, json.Number, bool, nil:
		return nil
	default:
		return fmt.Errorf("unexpected JSON token")
	}
	return nil
}

type validatedTarget struct {
	spec AdapterTarget
	base BaseLayer
}

func validateAdapterManifest(
	manifest AdapterManifest,
	base *BaseModel,
	policy SecurityPolicy,
	maxRank int,
	allowed map[DType]struct{},
	maxScaleNumerator int64,
	maxScaleDenominator int64,
) ([]validatedTarget, error) {
	if manifest.AdapterDigest == "" {
		return nil, fmt.Errorf("%w: empty adapter digest", ErrInvalidManifest)
	}
	if manifest.BaseModelDigest != base.ModelDigest() {
		return nil, fmt.Errorf(
			"%w: got %q, want %q",
			ErrBaseDigestMismatch,
			manifest.BaseModelDigest,
			base.ModelDigest(),
		)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return nil, fmt.Errorf("%w: empty adapter version", ErrInvalidManifest)
	}
	if len(manifest.Targets) == 0 {
		return nil, fmt.Errorf("%w: adapter has no targets", ErrInvalidManifest)
	}

	targets := make([]validatedTarget, 0, len(manifest.Targets))
	seen := make(map[string]struct{}, len(manifest.Targets))
	securityAllowed := policy != nil && policy.AllowSecurityCritical(manifest.AdapterDigest)

	for _, target := range manifest.Targets {
		if target.LayerID == "" {
			return nil, fmt.Errorf("%w: empty target layer ID", ErrInvalidManifest)
		}
		if _, duplicate := seen[target.LayerID]; duplicate {
			return nil, layerError(target.LayerID, ErrDuplicateLayer)
		}
		seen[target.LayerID] = struct{}{}

		baseLayer, exists := base.Layer(target.LayerID)
		if !exists {
			return nil, layerError(target.LayerID, ErrLayerNotFound)
		}
		if target.Rank < 1 || target.Rank > maxRank {
			return nil, layerError(target.LayerID, fmt.Errorf("%w: rank %d not in [1,%d]", ErrRankOutOfRange, target.Rank, maxRank))
		}
		if target.ADigest == "" || target.BDigest == "" {
			return nil, layerError(target.LayerID, fmt.Errorf("%w: empty factor digest", ErrInvalidManifest))
		}
		if _, ok := allowed[target.DType]; !ok {
			return nil, layerError(target.LayerID, fmt.Errorf("%w: %q", ErrDTypeNotAllowed, target.DType))
		}
		if _, ok := target.DType.elementSize(); !ok {
			return nil, layerError(target.LayerID, fmt.Errorf("%w: unsupported dtype %q", ErrDTypeNotAllowed, target.DType))
		}
		if baseLayer.SecurityCritical && !securityAllowed {
			return nil, layerError(target.LayerID, fmt.Errorf("%w: adapter %q", ErrSecurityCritical, manifest.AdapterDigest))
		}
		if err := validateScale(target.Scale, maxScaleNumerator, maxScaleDenominator); err != nil {
			return nil, layerError(target.LayerID, err)
		}
		targets = append(targets, validatedTarget{spec: target, base: baseLayer})
	}
	return targets, nil
}

func validateScale(scale Rational, maxNumerator, maxDenominator int64) error {
	if scale.Denominator <= 0 {
		return fmt.Errorf("%w: denominator must be positive", ErrScaleOutOfRange)
	}
	if maxNumerator <= 0 || maxDenominator <= 0 {
		return fmt.Errorf("%w: invalid configured bounds", ErrInvalidConfig)
	}

	numerator := absInt64(scale.Numerator)
	denominator := uint64(scale.Denominator)
	if numerator > uint64(maxNumerator) || denominator > uint64(maxDenominator) {
		return fmt.Errorf("%w: %s", ErrScaleOutOfRange, scale)
	}
	if gcd(numerator, denominator) != 1 {
		return fmt.Errorf("%w: %s", ErrScaleNotReduced, scale)
	}
	return nil
}

func absInt64(value int64) uint64 {
	if value >= 0 {
		return uint64(value)
	}
	return uint64(-(value + 1)) + 1
}

func gcd(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
