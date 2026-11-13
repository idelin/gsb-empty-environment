package netsec

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Options controls verification. The zero value implements the safe default:
// digest-derived positions, a contiguous run of disclosed positions, and no
// requirement that hidden elements be revealed.
type Options struct {
	// Algorithm selects the disclosure digest algorithm. The empty value
	// means SHA-256.
	Algorithm HashAlgorithm
	// AllowSparse permits gaps between disclosed positions. It is false by
	// default because a moved disclosure commonly leaves a gap behind.
	AllowSparse bool
	// RequireAll requires every commitment position to be disclosed.
	RequireAll bool
	// IgnoreIndexHints derives the position only from the digest and does
	// not compare it with Disclosure.Index. This is useful for formats that
	// do not carry presentation indices, but it is not the default.
	IgnoreIndexHints bool
}

// VerifyArray verifies indexed disclosures against a signed array commitment.
// It returns the reconstructed array in signed order. Positions that were not
// disclosed retain their commitment digest.
//
// commitment accepts []string or []any whose entries are digest strings or
// {"...": digest} objects. disclosures accepts []Disclosure, []string,
// map[int]Disclosure, map[int]string, or a []any containing any of the
// supported single-disclosure forms.
func VerifyArray(commitment any, disclosures any) ([]any, error) {
	return VerifyArrayWithOptions(commitment, disclosures, Options{})
}

// VerifyArrayDisclosures is an alias for VerifyArray.
func VerifyArrayDisclosures(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// VerifyArrayDisclosure is a singular alias for VerifyArray.
func VerifyArrayDisclosure(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// VerifyArrayElementDisclosures is an alias for VerifyArray.
func VerifyArrayElementDisclosures(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// ResolveArrayDisclosures is an alias for VerifyArray.
func ResolveArrayDisclosures(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// ResolveArray is an alias for VerifyArray.
func ResolveArray(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// ApplyArrayDisclosures is an alias for VerifyArray.
func ApplyArrayDisclosures(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// ApplyDisclosures is an alias for VerifyArray.
func ApplyDisclosures(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// ReconstructArray is an alias for VerifyArray.
func ReconstructArray(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// Reconstruct is an alias for VerifyArray.
func Reconstruct(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// Verify is an alias for VerifyArray.
func Verify(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// VerifyPresentation is an alias for VerifyArray.
func VerifyPresentation(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// ValidateArray checks a presentation and discards the reconstructed array.
func ValidateArray(commitment any, disclosures any) error {
	_, err := VerifyArray(commitment, disclosures)
	return err
}

// Validate is an alias for ValidateArray.
func Validate(commitment any, disclosures any) error {
	return ValidateArray(commitment, disclosures)
}

// CheckArray is an alias for ValidateArray.
func CheckArray(commitment any, disclosures any) error {
	return ValidateArray(commitment, disclosures)
}

// Check is an alias for ValidateArray.
func Check(commitment any, disclosures any) error {
	return ValidateArray(commitment, disclosures)
}

// VerifyEncodedArray verifies raw encoded disclosures. Since raw disclosures
// carry no index claim, their positions are always derived from the digest.
// The order of the disclosures is irrelevant.
func VerifyEncodedArray(commitment []string, disclosures []string) ([]any, error) {
	converted := make([]Disclosure, 0, len(disclosures))
	for _, encoded := range disclosures {
		converted = append(converted, Disclosure{Index: -1, Disclosure: encoded})
	}
	return VerifyArrayWithOptions(commitment, converted, Options{IgnoreIndexHints: true})
}

// VerifyDisclosures is an alias for VerifyEncodedArray.
func VerifyDisclosures(commitment any, disclosures any) ([]any, error) {
	return VerifyArray(commitment, disclosures)
}

// VerifyArrayStrings verifies indexed disclosures and returns string values.
// It is a convenience for arrays whose elements are known to be strings.
func VerifyArrayStrings(commitment any, disclosures any) ([]string, error) {
	values, err := VerifyArray(commitment, disclosures)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(values))
	for index, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: position %d is %T, not string", ErrInvalidDisclosure, index, value)
		}
		result[index] = text
	}
	return result, nil
}

// VerifyStrings is an alias for VerifyArrayStrings.
func VerifyStrings(commitment any, disclosures any) ([]string, error) {
	return VerifyArrayStrings(commitment, disclosures)
}

// VerifyArrayMap verifies an index-to-disclosure map. A map has no implicit
// ordering, so every key is treated as an explicit index claim.
func VerifyArrayMap(commitment []string, disclosures map[int]Disclosure) ([]any, error) {
	indices := make([]int, 0, len(disclosures))
	for index := range disclosures {
		indices = append(indices, index)
	}
	sort.Ints(indices)

	ordered := make([]Disclosure, 0, len(indices))
	for _, index := range indices {
		disclosure := disclosures[index]
		disclosure.Index = index
		disclosure.IndexSet = true
		disclosure.Position = 0
		disclosure.ArrayIndex = 0
		ordered = append(ordered, disclosure)
	}
	return VerifyArray(commitment, ordered)
}

// VerifyArrayIndexMap verifies a map from claimed index to encoded disclosure.
func VerifyArrayIndexMap(commitment []string, disclosures map[int]string) ([]any, error) {
	converted := make(map[int]Disclosure, len(disclosures))
	for index, encoded := range disclosures {
		converted[index] = Disclosure{Index: index, IndexSet: true, Disclosure: encoded}
	}
	return VerifyArrayMap(commitment, converted)
}

// VerifyArrayWithOptions verifies indexed disclosures with explicit options.
func VerifyArrayWithOptions(commitmentInput any, disclosuresInput any, options Options) ([]any, error) {
	commitment, err := commitmentInputStrings(commitmentInput)
	if err != nil {
		return nil, err
	}
	disclosures, ignoreIndexHints, err := disclosureInputs(disclosuresInput)
	if err != nil {
		return nil, err
	}
	if ignoreIndexHints {
		options.IgnoreIndexHints = true
	}

	algorithm, err := normalizeAlgorithm(options.Algorithm)
	if err != nil {
		return nil, err
	}

	commitmentBytes := make([][]byte, len(commitment))
	seenCommitment := make(map[string]int, len(commitment))
	for index, digest := range commitment {
		decoded, err := decodeDigestValue(digest)
		if err != nil {
			return nil, fmt.Errorf("%w: position %d: %v", ErrInvalidCommitment, index, err)
		}
		key := string(decoded)
		if previous, exists := seenCommitment[key]; exists {
			return nil, fmt.Errorf("%w: positions %d and %d", ErrDuplicateDigest, previous, index)
		}
		seenCommitment[key] = index
		commitmentBytes[index] = decoded
	}

	result := make([]any, len(commitment))
	for index, digest := range commitment {
		result[index] = digest
	}

	usedPositions := make(map[int]struct{}, len(disclosures))
	claimedPositions := make([]int, 0, len(disclosures))

	for disclosureIndex, disclosure := range disclosures {
		decoded, err := decodeDisclosure(disclosure)
		if err != nil {
			return nil, fmt.Errorf("%w: disclosure %d: %v", ErrInvalidDisclosure, disclosureIndex, err)
		}

		position, err := locateDisclosure(commitmentBytes, decoded, disclosure, algorithm)
		if err != nil {
			return nil, fmt.Errorf("disclosure %d: %w", disclosureIndex, err)
		}

		claimedIndex, hasIndexClaim, err := disclosure.indexClaim()
		if err != nil {
			return nil, fmt.Errorf("disclosure %d: %w", disclosureIndex, err)
		}
		if !options.IgnoreIndexHints && hasIndexClaim && claimedIndex != position {
			return nil, fmt.Errorf(
				"%w: disclosure %d claims index %d but signed position is %d",
				ErrIndexMismatch, disclosureIndex, claimedIndex, position,
			)
		}

		if _, exists := usedPositions[position]; exists {
			return nil, fmt.Errorf("%w: multiple disclosures resolve to position %d", ErrDuplicateDisclosure, position)
		}
		usedPositions[position] = struct{}{}
		claimedPositions = append(claimedPositions, position)
		result[position] = decoded.value
	}

	sort.Ints(claimedPositions)
	if !options.AllowSparse {
		for index := 1; index < len(claimedPositions); index++ {
			if claimedPositions[index] != claimedPositions[index-1]+1 {
				return nil, fmt.Errorf(
					"%w: positions %d and %d are separated by a gap",
					ErrNonContiguousIndices, claimedPositions[index-1], claimedPositions[index],
				)
			}
		}
	}

	if options.RequireAll {
		for position := range commitment {
			if _, exists := usedPositions[position]; !exists {
				return nil, fmt.Errorf("%w: position %d", ErrMissingDisclosure, position)
			}
		}
	}

	return result, nil
}

func commitmentInputStrings(input any) ([]string, error) {
	switch value := input.(type) {
	case nil:
		return nil, nil
	case []string:
		return value, nil
	case []any:
		return commitmentStrings(value)
	default:
		return nil, fmt.Errorf("%w: unsupported commitment type %T", ErrInvalidCommitment, input)
	}
}

func disclosureInputs(input any) ([]Disclosure, bool, error) {
	switch value := input.(type) {
	case nil:
		return nil, false, nil
	case []Disclosure:
		return value, false, nil
	case []string:
		converted := make([]Disclosure, 0, len(value))
		for _, encoded := range value {
			converted = append(converted, Disclosure{Index: -1, Disclosure: encoded})
		}
		return converted, true, nil
	case map[int]Disclosure:
		indices := make([]int, 0, len(value))
		for index := range value {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		converted := make([]Disclosure, 0, len(indices))
		for _, index := range indices {
			disclosure := value[index]
			disclosure.Index = index
			disclosure.IndexSet = true
			disclosure.Position = 0
			disclosure.ArrayIndex = 0
			converted = append(converted, disclosure)
		}
		return converted, false, nil
	case map[int]string:
		converted := make(map[int]Disclosure, len(value))
		for index, encoded := range value {
			converted[index] = Disclosure{Index: index, IndexSet: true, Disclosure: encoded}
		}
		return disclosureInputs(converted)
	case []any:
		converted := make([]Disclosure, 0, len(value))
		for index, element := range value {
			switch item := element.(type) {
			case Disclosure:
				converted = append(converted, item)
			case string:
				converted = append(converted, Disclosure{Index: -1, Disclosure: item})
			case map[string]any:
				encoded, err := json.Marshal(item)
				if err != nil {
					return nil, false, fmt.Errorf("%w: disclosure %d: %v", ErrInvalidDisclosure, index, err)
				}
				var disclosure Disclosure
				if err := json.Unmarshal(encoded, &disclosure); err != nil {
					return nil, false, fmt.Errorf("%w: disclosure %d: %v", ErrInvalidDisclosure, index, err)
				}
				converted = append(converted, disclosure)
			default:
				return nil, false, fmt.Errorf("%w: disclosure %d has unsupported type %T", ErrInvalidDisclosure, index, element)
			}
		}
		return converted, false, nil
	default:
		return nil, false, fmt.Errorf("%w: unsupported disclosures type %T", ErrInvalidDisclosure, input)
	}
}

func locateDisclosure(commitment [][]byte, decoded decodedDisclosure, disclosure Disclosure, algorithm HashAlgorithm) (int, error) {
	claimedDigest := strings.TrimSpace(disclosure.Digest)
	if claimedDigest != "" && decoded.hasValue {
		claimedBytes, err := decodeDigestValue(claimedDigest)
		if err != nil {
			return 0, fmt.Errorf("%w: invalid digest: %v", ErrDigestMismatch, err)
		}
		if !digestSatisfiesAny(decoded, algorithm, claimedBytes) {
			return 0, fmt.Errorf("%w: claimed digest does not match disclosure", ErrDigestMismatch)
		}
	}

	matches := make([]int, 0, 1)
	for position, committed := range commitment {
		if decoded.hasValue {
			if digestSatisfiesAny(decoded, algorithm, committed) {
				matches = append(matches, position)
			}
			continue
		}
		if claimedDigest != "" {
			claimedBytes, err := decodeDigestValue(claimedDigest)
			if err == nil && digestMatches(claimedBytes, committed) {
				matches = append(matches, position)
			}
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("%w: %w", ErrUnmatchedDisclosure, ErrDigestMismatch)
	case 1:
		return matches[0], nil
	default:
		return 0, ErrAmbiguousDigest
	}
}

func digestSatisfiesAny(decoded decodedDisclosure, algorithm HashAlgorithm, expected []byte) bool {
	algorithms := make([]HashAlgorithm, 0, 3)
	algorithms = append(algorithms, algorithm)
	if len(expected) == sha256Size && algorithm != SHA256 {
		algorithms = []HashAlgorithm{SHA256}
	}
	if len(expected) == sha384Size {
		algorithms = append(algorithms, SHA384)
	}
	if len(expected) == sha512Size {
		algorithms = append(algorithms, SHA512)
	}

	seen := make(map[HashAlgorithm]struct{}, len(algorithms))
	for _, candidateAlgorithm := range algorithms {
		if _, exists := seen[candidateAlgorithm]; exists {
			continue
		}
		seen[candidateAlgorithm] = struct{}{}
		for _, input := range decoded.digestCandidates() {
			if digestMatches(digestBytes(input, candidateAlgorithm), expected) {
				return true
			}
		}
	}
	return false
}

const (
	sha256Size = 32
	sha384Size = 48
	sha512Size = 64
)
