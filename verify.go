package netsec

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const supportedSDAlg = "sha-256"

type config struct {
	requireSDAlg bool
}

// Option customizes verification. The defaults follow the SD-JWT rule that an
// absent _sd_alg means sha-256.
type Option func(*config)

// WithRequireSDAlg requires an explicit _sd_alg member. When present, _sd_alg
// is always required to equal "sha-256" exactly.
func WithRequireSDAlg(required bool) Option {
	return func(c *config) {
		c.requireSDAlg = required
	}
}

// DisclosedClaim describes a disclosure that was applied to the payload.
type DisclosedClaim struct {
	Digest string         `json:"digest"`
	Name   string         `json:"name,omitempty"`
	Value  any            `json:"value"`
	Path   string         `json:"path"`
	Kind   DisclosureKind `json:"kind"`
}

// UndisclosedDigest describes an _sd entry that has no matching disclosure.
// Such entries are intentionally left in the returned payload; this is how a
// decoy, or an intentionally hidden claim, remains explicitly undisclosed.
type UndisclosedDigest struct {
	Digest string `json:"digest"`
	Path   string `json:"path"`
	Layer  string `json:"layer"`
}

// Result is the outcome of verification.
type Result struct {
	Claims      map[string]any      `json:"claims"`
	Disclosed   []DisclosedClaim    `json:"disclosed"`
	Undisclosed []UndisclosedDigest `json:"undisclosed"`
}

// Verify checks disclosures against payload and returns the claims with every
// matching disclosure applied recursively. The input payload is not mutated.
//
// A digest may occur only once in the whole document, across all _sd arrays
// and array-element slots. Consequently a digest that is used as a decoy in
// one place and as a real disclosure in another is rejected. Every _sd layer
// reachable from the payload is visited; entries that are not disclosed are
// kept in the result.
func Verify(payload map[string]any, disclosures []string, opts ...Option) (map[string]any, error) {
	result, err := VerifyDetailed(payload, disclosures, opts...)
	if err != nil {
		return nil, err
	}
	return result.Claims, nil
}

// VerifyDetailed is Verify with a report of disclosed and undisclosed slots.
func VerifyDetailed(payload map[string]any, disclosures []string, opts ...Option) (*Result, error) {
	if payload == nil {
		return nil, fmt.Errorf("%w: payload is nil", ErrMalformedPayload)
	}

	cfg := config{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	root, err := cloneObject(payload)
	if err != nil {
		return nil, err
	}

	v := newVerifier(cfg)
	if err := v.addDisclosures(disclosures); err != nil {
		return nil, err
	}
	if err := v.walkObject(root); err != nil {
		return nil, err
	}
	if err := v.resolve(); err != nil {
		return nil, err
	}

	return &Result{
		Claims:      root.value,
		Disclosed:   v.disclosed,
		Undisclosed: v.undisclosed(),
	}, nil
}

// VerifySDJWT is an alias for VerifyDetailed.
func VerifySDJWT(payload map[string]any, disclosures []string, opts ...Option) (*Result, error) {
	return VerifyDetailed(payload, disclosures, opts...)
}

// VerifyDisclosures is an alias for VerifyDetailed.
func VerifyDisclosures(payload map[string]any, disclosures []string, opts ...Option) (*Result, error) {
	return VerifyDetailed(payload, disclosures, opts...)
}

// Validate verifies the disclosures and discards the resulting claims.
func Validate(payload map[string]any, disclosures []string, opts ...Option) error {
	_, err := VerifyDetailed(payload, disclosures, opts...)
	return err
}

// VerifyJSON decodes a JSON claims object, verifies it, and returns the
// verified claims.
func VerifyJSON(payloadJSON []byte, disclosures []string, opts ...Option) (map[string]any, error) {
	payload, err := decodeObject(payloadJSON)
	if err != nil {
		return nil, err
	}
	return Verify(payload, disclosures, opts...)
}

// VerifyJSONDetailed decodes a JSON claims object and returns the full report.
func VerifyJSONDetailed(payloadJSON []byte, disclosures []string, opts ...Option) (*Result, error) {
	payload, err := decodeObject(payloadJSON)
	if err != nil {
		return nil, err
	}
	return VerifyDetailed(payload, disclosures, opts...)
}

// VerifyCompact verifies an SD-JWT compact serialization. The first
// tilde-separated part is the JWT; only its payload is inspected, because this
// library verifies selective-disclosure semantics rather than signatures.
func VerifyCompact(serialized string, opts ...Option) (*Result, error) {
	parts := strings.Split(strings.TrimSpace(serialized), "~")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("%w: empty compact SD-JWT", ErrMalformedPayload)
	}

	payload, err := jwtPayload(parts[0])
	if err != nil {
		return nil, err
	}

	disclosures := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		if part != "" {
			disclosures = append(disclosures, part)
		}
	}
	return VerifyDetailed(payload, disclosures, opts...)
}

type payloadNode struct {
	value map[string]any
	path  string
}

type slotKind uint8

const (
	objectSlot slotKind = iota
	arraySlot
)

type sdEntry struct {
	digest string
	used   bool
	index  int
}

type sdArray struct {
	owner   map[string]any
	path    string
	entries []*sdEntry
}

type digestSlot struct {
	digest string
	kind   slotKind
	path   string

	// Object-property slots.
	sdArray    *sdArray
	objectPath string

	// Array-element slots.
	array      []any
	arrayIndex int
}

type verifier struct {
	cfg config

	digests map[string]*digestSlot
	arrays  []*sdArray

	disclosureByDigest map[string]*Disclosure
	disclosures        []*Disclosure
	used               map[string]bool
	disclosed          []DisclosedClaim
}

func newVerifier(cfg config) *verifier {
	return &verifier{
		cfg:                cfg,
		digests:            make(map[string]*digestSlot),
		disclosureByDigest: make(map[string]*Disclosure),
		used:               make(map[string]bool),
	}
}

func (v *verifier) addDisclosures(raw []string) error {
	for _, list := range raw {
		for _, value := range strings.Split(list, "~") {
			if strings.TrimSpace(value) == "" {
				continue
			}
			d, err := ParseDisclosure(value)
			if err != nil {
				return err
			}
			if previous, ok := v.disclosureByDigest[d.Digest]; ok {
				return fmt.Errorf("%w: %s (already supplied by %s)", ErrDuplicateDisclosure, d.Digest, previous.Raw)
			}
			v.disclosureByDigest[d.Digest] = d
			v.disclosures = append(v.disclosures, d)
		}
	}
	return nil
}

func (v *verifier) walkObject(node *payloadNode) error {
	if alg, ok := node.value["_sd_alg"]; ok {
		if err := v.checkAlgorithm(node, alg); err != nil {
			return err
		}
	} else if v.cfg.requireSDAlg && node.path == "" {
		return fmt.Errorf("%w at %s: _sd_alg is required", ErrUnsupportedAlgorithm, pointer(node.path))
	}

	if raw, ok := node.value["_sd"]; ok {
		entries, err := normalizeSD(node.path, raw)
		if err != nil {
			return err
		}
		layerPath := joinPointer(node.path, "_sd")
		arr := &sdArray{owner: node.value, path: layerPath}
		for i, digest := range entries {
			entry := &sdEntry{digest: digest, index: i}
			arr.entries = append(arr.entries, entry)
			if err := v.addSlot(&digestSlot{
				digest:     digest,
				kind:       objectSlot,
				path:       joinPointer(pointer(layerPath), fmt.Sprintf("%d", i)),
				sdArray:    arr,
				objectPath: node.path,
			}); err != nil {
				return err
			}
		}
		v.arrays = append(v.arrays, arr)
	}

	keys := make([]string, 0, len(node.value))
	for key := range node.value {
		if key == "_sd" || key == "_sd_alg" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := v.walkValue(node.value[key], joinPointer(node.path, key)); err != nil {
			return err
		}
	}
	return nil
}

func (v *verifier) walkValue(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		return v.walkObject(&payloadNode{value: typed, path: path})
	case []any:
		for i, element := range typed {
			elementPath := joinPointer(path, fmt.Sprintf("%d", i))
			switch item := element.(type) {
			case map[string]any:
				if len(item) == 1 {
					if digest, ok := item["..."]; ok {
						if value, ok := digest.(string); ok && value != "" {
							if err := v.addSlot(&digestSlot{
								digest:     value,
								kind:       arraySlot,
								path:       elementPath,
								array:      typed,
								arrayIndex: i,
							}); err != nil {
								return err
							}
						}
					}
				}
				if err := v.walkObject(&payloadNode{value: item, path: elementPath}); err != nil {
					return err
				}
			case []any:
				if err := v.walkValue(item, elementPath); err != nil {
					return err
				}
			case string:
				// Array-element disclosures are identified by a digest string.
				// Only strings that are actually disclosed are treated as slots;
				// this avoids confusing ordinary strings with digests.
				if _, ok := v.disclosureByDigest[item]; ok {
					if err := v.addSlot(&digestSlot{
						digest:     item,
						kind:       arraySlot,
						path:       elementPath,
						array:      typed,
						arrayIndex: i,
					}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	default:
		return nil
	}
}

func (v *verifier) checkAlgorithm(node *payloadNode, value any) error {
	algorithm, ok := value.(string)
	if !ok || algorithm != supportedSDAlg {
		return fmt.Errorf("%w at %s: only %q is accepted, got %s",
			ErrUnsupportedAlgorithm, pointer(joinPointer(node.path, "_sd_alg")), supportedSDAlg, compactJSON(value))
	}
	return nil
}

func (v *verifier) addSlot(slot *digestSlot) error {
	if previous, ok := v.digests[slot.digest]; ok {
		return fmt.Errorf("%w: %s occurs at %s and %s",
			ErrDuplicateDigest, slot.digest, previous.path, slot.path)
	}
	v.digests[slot.digest] = slot
	return nil
}

func (v *verifier) resolve() error {
	for {
		progress := false
		for _, d := range v.disclosures {
			if v.used[d.Digest] {
				continue
			}
			slot, ok := v.digests[d.Digest]
			if !ok {
				continue
			}
			if err := v.applyDisclosure(slot, d); err != nil {
				return err
			}
			v.used[d.Digest] = true
			progress = true
		}
		if !progress {
			break
		}
	}

	for _, d := range v.disclosures {
		if !v.used[d.Digest] {
			return fmt.Errorf("%w: %s", ErrUnmatchedDisclosure, d.Raw)
		}
	}

	for _, arr := range v.arrays {
		kept := make([]any, 0, len(arr.entries))
		for _, entry := range arr.entries {
			if !entry.used {
				kept = append(kept, entry.digest)
			}
		}
		arr.owner["_sd"] = kept
	}
	return nil
}

func (v *verifier) applyDisclosure(slot *digestSlot, d *Disclosure) error {
	switch slot.kind {
	case objectSlot:
		if d.Kind != ObjectDisclosure {
			return fmt.Errorf("%w at %s: object slot requires a three-element disclosure",
				ErrDisclosureKindMismatch, slot.path)
		}
		if _, exists := slot.sdArray.owner[d.Name]; exists {
			return fmt.Errorf("%w at %s: %q", ErrClaimConflict,
				pointer(joinPointer(slot.objectPath, d.Name)), d.Name)
		}
		slot.sdArray.owner[d.Name] = d.Value
		for _, entry := range slot.sdArray.entries {
			if entry.digest == d.Digest && !entry.used {
				entry.used = true
				break
			}
		}
		path := joinPointer(slot.objectPath, d.Name)
		v.disclosed = append(v.disclosed, DisclosedClaim{
			Digest: d.Digest,
			Name:   d.Name,
			Value:  d.Value,
			Path:   path,
			Kind:   d.Kind,
		})
		// A disclosed value can itself contain another _sd layer. Discover it
		// before the next resolve pass.
		return v.walkValue(d.Value, path)
	case arraySlot:
		if d.Kind != ArrayDisclosure {
			return fmt.Errorf("%w at %s: array slot requires a two-element disclosure",
				ErrDisclosureKindMismatch, slot.path)
		}
		slot.array[slot.arrayIndex] = d.Value
		v.disclosed = append(v.disclosed, DisclosedClaim{
			Digest: d.Digest,
			Value:  d.Value,
			Path:   slot.path,
			Kind:   d.Kind,
		})
		return v.walkValue(d.Value, slot.path)
	default:
		return fmt.Errorf("%w: unknown slot kind", ErrMalformedPayload)
	}
}

func (v *verifier) undisclosed() []UndisclosedDigest {
	out := make([]UndisclosedDigest, 0)
	for _, arr := range v.arrays {
		for _, entry := range arr.entries {
			if !entry.used {
				out = append(out, UndisclosedDigest{
					Digest: entry.digest,
					Path:   joinPointer(pointer(arr.path), fmt.Sprintf("%d", entry.index)),
					Layer:  pointer(arr.path),
				})
			}
		}
	}
	return out
}

func normalizeSD(path string, value any) ([]string, error) {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for i, item := range typed {
			digest, ok := item.(string)
			if !ok || digest == "" {
				return nil, fmt.Errorf("%w at %s: element %d is not a non-empty string",
					ErrMalformedSD, pointer(joinPointer(path, "_sd")), i)
			}
			out = append(out, digest)
		}
		return out, nil
	case []string:
		out := make([]string, 0, len(typed))
		for i, digest := range typed {
			if digest == "" {
				return nil, fmt.Errorf("%w at %s: element %d is empty",
					ErrMalformedSD, pointer(joinPointer(path, "_sd")), i)
			}
			out = append(out, digest)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w at %s: expected an array", ErrMalformedSD, pointer(joinPointer(path, "_sd")))
	}
}

func decodeObject(data []byte) (map[string]any, error) {
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedPayload, err)
	}
	if object == nil {
		return nil, fmt.Errorf("%w: expected a JSON object", ErrMalformedPayload)
	}
	return object, nil
}

func jwtPayload(jwt string) (map[string]any, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: JWT must have three segments", ErrMalformedPayload)
	}
	decoded, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid JWT payload encoding: %v", ErrMalformedPayload, err)
	}
	return decodeObject(decoded)
}

func pointer(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func joinPointer(base, segment string) string {
	if base == "" || base == "/" {
		base = ""
	}
	return base + "/" + strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}

// cloneObject makes a detached, JSON-shaped copy so the caller's payload is
// never modified while disclosures are applied.
func cloneObject(root map[string]any) (*payloadNode, error) {
	clone, err := cloneValue(root)
	if err != nil {
		return nil, err
	}
	object, ok := clone.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: payload must be an object", ErrMalformedPayload)
	}
	return &payloadNode{value: object, path: ""}, nil
}

func cloneValue(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			clone, err := cloneValue(item)
			if err != nil {
				return nil, err
			}
			out[key] = clone
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			clone, err := cloneValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = clone
		}
		return out, nil
	default:
		return typed, nil
	}
}
