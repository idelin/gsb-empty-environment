// Package include expands ordered includes across trusted and untrusted
// configuration documents for the policy service loader.
//
// Documents are supplied by the caller as an immutable map keyed by document
// ID; Resolve never mutates its input. Expansion is depth-first: a document's
// includes are expanded in order before the document's own settings are
// written, so later writes override earlier ones. A write made through an
// untrusted path (the document itself or any include ancestor is untrusted)
// can never override a key already written through a fully trusted path.
// Cycles abort resolution and yield no partial result.
package include

import (
	"fmt"
	"strings"
)

// MaxDocuments bounds the number of documents a single Resolve call accepts.
const MaxDocuments = 128

// Trust is the trust level attached to a document by the caller. Documents
// covered by signature protection are Trusted; anything an attacker may
// supply must be marked Untrusted.
type Trust int

const (
	Untrusted Trust = iota
	Trusted
)

// Document is a single configuration document. The caller provides documents
// as immutable values: Resolve never mutates Settings or Includes.
type Document struct {
	ID       string
	Trust    Trust
	Settings map[string]string
	Includes []string
}

// Source describes the winning write for an effective key.
type Source struct {
	Path    []string // include chain from the root document to the writer, inclusive
	Trusted bool     // effective trust of the winning write
}

// Rejection describes a denied override attempt: an untrusted path tried to
// overwrite a key currently held by a trusted path. Rejections are recorded
// even when the attempted value equals the current one.
type Rejection struct {
	Key       string
	Value     string   // attempted value
	Path      []string // include chain of the rejected write
	BlockedBy []string // trusted source path that held the key
}

// Result is the fully expanded configuration. It is only produced when the
// whole include graph resolves cleanly; cycles and unknown documents yield
// no partial Result.
type Result struct {
	Values  map[string]string
	Sources map[string]Source
	Rejects []Rejection
}

// CycleError reports an include cycle. Path is the complete include chain
// from the root document to the repeated document, inclusive.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "include: cycle detected: " + strings.Join(e.Path, " -> ")
}

// UnknownDocumentError reports an include target (or the root) that is not
// present in the provided document set.
type UnknownDocumentError struct {
	ID   string
	From []string // include chain of the referrer; empty for the root
}

func (e *UnknownDocumentError) Error() string {
	if len(e.From) == 0 {
		return fmt.Sprintf("include: unknown root document %q", e.ID)
	}
	return fmt.Sprintf("include: unknown document %q referenced by %s",
		e.ID, strings.Join(e.From, " -> "))
}

// TooManyDocumentsError reports a document set larger than MaxDocuments.
type TooManyDocumentsError struct {
	Count int
}

func (e *TooManyDocumentsError) Error() string {
	return fmt.Sprintf("include: %d documents exceed limit of %d", e.Count, MaxDocuments)
}

// Resolve expands the include graph rooted at root and returns the effective
// configuration. The same document may be reached through several include
// paths and is expanded once per occurrence, in its own trust context;
// results are never cached by document ID. Any cycle, unknown document, or
// invalid input aborts resolution and no partial configuration is published.
func Resolve(docs map[string]Document, root string) (*Result, error) {
	if len(docs) > MaxDocuments {
		return nil, &TooManyDocumentsError{Count: len(docs)}
	}
	for id, doc := range docs {
		if doc.ID != id {
			return nil, fmt.Errorf("include: document ID %q does not match map key %q", doc.ID, id)
		}
	}
	rootDoc, ok := docs[root]
	if !ok {
		return nil, &UnknownDocumentError{ID: root}
	}

	r := &Result{
		Values:  make(map[string]string),
		Sources: make(map[string]Source),
	}

	// Iterative depth-first expansion. Each stack frame is one document
	// occurrence; includes are expanded in order before the document's own
	// settings are written.
	type frame struct {
		id      string
		trusted bool // effective trust at this occurrence
		next    int  // next include index to expand
		applied bool // own settings already written
	}
	onPath := make(map[string]bool, len(docs))
	stack := make([]frame, 0, len(docs))
	path := make([]string, 0, len(docs)) // document IDs along stack

	push := func(id string, trusted bool) {
		onPath[id] = true
		stack = append(stack, frame{id: id, trusted: trusted})
		path = append(path, id)
	}
	push(root, rootDoc.Trust == Trusted)

	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		doc := docs[top.id]
		if top.next < len(doc.Includes) {
			childID := doc.Includes[top.next]
			top.next++
			child, ok := docs[childID]
			if !ok {
				return nil, &UnknownDocumentError{
					ID:   childID,
					From: append([]string(nil), path...),
				}
			}
			if onPath[childID] {
				cycle := append([]string(nil), path...)
				return nil, &CycleError{Path: append(cycle, childID)}
			}
			push(childID, top.trusted && child.Trust == Trusted)
			continue
		}
		if !top.applied {
			for key, value := range doc.Settings {
				r.write(key, value, top.trusted, path)
			}
			top.applied = true
			continue
		}
		delete(onPath, top.id)
		stack = stack[:len(stack)-1]
		path = path[:len(path)-1]
	}

	return r, nil
}

// write applies a single setting written through path with the given
// effective trust. A later write overrides an earlier one unless the earlier
// write is trusted and the later one is not.
func (r *Result) write(key, value string, trusted bool, path []string) {
	if src, exists := r.Sources[key]; exists && src.Trusted && !trusted {
		r.Rejects = append(r.Rejects, Rejection{
			Key:       key,
			Value:     value,
			Path:      append([]string(nil), path...),
			BlockedBy: append([]string(nil), src.Path...),
		})
		return
	}
	r.Values[key] = value
	r.Sources[key] = Source{
		Path:    append([]string(nil), path...),
		Trusted: trusted,
	}
}
