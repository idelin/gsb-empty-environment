package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Limits guarding the resolver against hostile inputs. Documents come from
// callers as an immutable map of at most MaxDocuments entries; expansion is
// deliberately not cached by document ID (the same document may be reached
// through paths of different trust and must be re-expanded each time), so
// MaxExpandSteps bounds the total expansion work to keep diamond-heavy
// graphs from exhausting the loader.
const (
	MaxDocuments   = 128
	MaxExpandSteps = 1 << 16
)

// TrustLevel classifies the origin of a configuration document.
type TrustLevel int

const (
	// Untrusted marks tenant-supplied documents; an attacker controls these
	// documents and their include relations.
	Untrusted TrustLevel = iota
	// Trusted marks signature-protected documents.
	Trusted
)

func (l TrustLevel) String() string {
	switch l {
	case Trusted:
		return "trusted"
	case Untrusted:
		return "untrusted"
	default:
		return fmt.Sprintf("TrustLevel(%d)", int(l))
	}
}

// Document is a single configuration document. The map key under which a
// Document is stored is its canonical ID and must match ID.
type Document struct {
	ID       string
	Trust    TrustLevel
	Settings map[string]string
	Includes []string
}

// EffectiveValue is the winning value for a key after resolution.
type EffectiveValue struct {
	Value string
	// Source is the include path from the root document to the document
	// occurrence that wrote the value.
	Source []string
	// Trusted reports whether the winning write happened on a fully trusted
	// path (the writing document and every include ancestor are trusted).
	Trusted bool
}

// DeniedOverride records a rejected write: an untrusted path attempted to
// override a key previously written by a trusted path.
type DeniedOverride struct {
	Key             string
	AttemptedValue  string
	AttemptedSource []string
	KeptValue       string
	KeptSource      []string
}

// Resolution is the outcome of a successful Resolve call.
type Resolution struct {
	// Values maps each key to its effective value and source path.
	Values map[string]EffectiveValue
	// Denied lists rejected override attempts in expansion order.
	Denied []DeniedOverride
}

// CycleError reports an include cycle. Path is the full include chain from
// the root to the repeated document; the first document of the cycle appears
// twice, at its first occurrence and again at the end.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "include cycle: " + strings.Join(e.Path, " -> ")
}

// UnknownDocumentError reports a reference to a document ID that is not
// present in the provided map.
type UnknownDocumentError struct {
	From string // empty when the root ID itself is unknown
	ID   string
}

func (e *UnknownDocumentError) Error() string {
	if e.From == "" {
		return fmt.Sprintf("unknown root document %q", e.ID)
	}
	return fmt.Sprintf("document %q includes unknown document %q", e.From, e.ID)
}

// InconsistentIDError reports a document whose ID field does not match the
// map key it is stored under.
type InconsistentIDError struct {
	Key string
	ID  string
}

func (e *InconsistentIDError) Error() string {
	return fmt.Sprintf("document stored under key %q declares ID %q", e.Key, e.ID)
}

var (
	// ErrTooManyDocuments is returned when more than MaxDocuments documents
	// are provided.
	ErrTooManyDocuments = errors.New("too many documents")
	// ErrTooManyExpansions is returned when expansion exceeds
	// MaxExpandSteps; because results are never cached by document ID,
	// pathological diamond graphs could otherwise expand without bound.
	ErrTooManyExpansions = errors.New("include expansion limit exceeded")
)

// Resolve expands the include graph rooted at rootID and merges settings.
//
// Expansion is depth-first: a document's includes are expanded in order
// first, then the document's own settings are applied, so later writes
// override earlier ones. A write only loses to a later write when the later
// write is trusted or the current holder is untrusted; an untrusted path can
// never override a key written by a trusted path. A trusted document is
// demoted whenever any include ancestor on the path is untrusted.
//
// Results are never cached by document ID: the same document reached through
// different trust paths is expanded once per occurrence.
//
// On any error (cycle, unknown document, limits) no partial configuration is
// published: Resolve returns a nil *Resolution.
func Resolve(docs map[string]Document, rootID string) (*Resolution, error) {
	if len(docs) > MaxDocuments {
		return nil, fmt.Errorf("%w: got %d, max %d", ErrTooManyDocuments, len(docs), MaxDocuments)
	}
	if err := validate(docs); err != nil {
		return nil, err
	}
	if _, ok := docs[rootID]; !ok {
		return nil, &UnknownDocumentError{ID: rootID}
	}
	r := &resolver{docs: docs, res: &Resolution{Values: make(map[string]EffectiveValue)}}
	if err := r.expand(rootID, nil, true); err != nil {
		return nil, err
	}
	return r.res, nil
}

func validate(docs map[string]Document) error {
	for key, doc := range docs {
		if doc.ID != key {
			return &InconsistentIDError{Key: key, ID: doc.ID}
		}
		for _, inc := range doc.Includes {
			if _, ok := docs[inc]; !ok {
				return &UnknownDocumentError{From: key, ID: inc}
			}
		}
	}
	return nil
}

type resolver struct {
	docs  map[string]Document
	res   *Resolution
	steps int
}

func (r *resolver) expand(id string, stack []string, trusted bool) error {
	for _, ancestor := range stack {
		if ancestor == id {
			path := make([]string, len(stack)+1)
			copy(path, stack)
			path[len(stack)] = id
			return &CycleError{Path: path}
		}
	}
	r.steps++
	if r.steps > MaxExpandSteps {
		return ErrTooManyExpansions
	}
	doc := r.docs[id]
	effective := trusted && doc.Trust == Trusted
	path := make([]string, len(stack)+1)
	copy(path, stack)
	path[len(stack)] = id
	for _, inc := range doc.Includes {
		if err := r.expand(inc, path, effective); err != nil {
			return err
		}
	}
	for _, key := range sortedKeys(doc.Settings) {
		r.apply(key, doc.Settings[key], effective, path)
	}
	return nil
}

func (r *resolver) apply(key, value string, trusted bool, path []string) {
	current, ok := r.res.Values[key]
	if ok && current.Trusted && !trusted {
		r.res.Denied = append(r.res.Denied, DeniedOverride{
			Key:             key,
			AttemptedValue:  value,
			AttemptedSource: path,
			KeptValue:       current.Value,
			KeptSource:      current.Source,
		})
		return
	}
	r.res.Values[key] = EffectiveValue{Value: value, Source: path, Trusted: trusted}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
