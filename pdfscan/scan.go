package pdfscan

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
)

const (
	maxObjects       = 1_000_000
	maxXrefRevisions = 64
	maxDecodedBytes  = 256 << 20
	maxWalkDepth     = 512
)

// Finding describes one rejected automatic/executable construct.
type Finding struct {
	// ObjectNum is the indirect object that carries the construct. It is
	// always non-zero for objects reachable through the catalog graph.
	ObjectNum int
	// Kind identifies the rejected construct.
	Kind FindingKind
	// Key is the normalized dictionary key where it was found, e.g.
	// "OpenAction", "AA", "JS" or "S".
	Key string
}

type FindingKind string

const (
	FindOpenAction FindingKind = "open-action"
	FindAdditional FindingKind = "additional-action"
	FindLaunch     FindingKind = "launch"
	FindJavaScript FindingKind = "javascript"
	FindSubmitForm FindingKind = "submit-form"
)

// Report is the result of a structural scan.
type Report struct {
	Findings []Finding
}

// Blocked reports whether the gateway must reject the document.
func (r *Report) Blocked() bool { return len(r.Findings) > 0 }

type scanner struct {
	store     *store
	findings  []Finding
	seen      map[visitKey]bool
	dedup     map[string]bool
	walkCount int
}

type visitKey struct {
	objNum int
	role   string
}

// ScanBytes performs a structural-only inspection of a PDF document. It
// never renders pages or executes anything. A non-nil error means the
// document could not be safely parsed and the gateway should fail closed;
// otherwise inspect Report.Blocked and the returned object numbers.
func ScanBytes(data []byte) (*Report, error) {
	if len(data) == 0 {
		return nil, errors.New("pdfscan: empty document")
	}
	if !looksLikePDF(data) {
		return nil, errors.New("pdfscan: not a PDF document (missing %PDF header)")
	}

	info, err := parseXref(data)
	if err != nil {
		// Last-resort: some handcrafted/corrupt documents omit usable
		// cross-references. Carve the objects and locate the catalog via a
		// trailer/Root search instead of giving up.
		recovered, recErr := recoverWithoutXref(data, err)
		if recErr != nil {
			return nil, err
		}
		info = recovered
	}
	if info.encrypted {
		return nil, errors.New("pdfscan: encrypted PDFs are not inspected (fail closed)")
	}

	st, err := newStore(data, info)
	if err != nil {
		return nil, err
	}

	s := &scanner{
		store: st,
		seen:  map[visitKey]bool{},
		dedup: map[string]bool{},
	}
	s.walk(st.root, "catalog", 0, st.root.ObjNum)

	return &Report{Findings: s.findings}, nil
}

// ScanReader buffers r and runs ScanBytes.
func ScanReader(r io.Reader) (*Report, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxDecodedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDecodedBytes {
		return nil, errors.New("pdfscan: document exceeds size limit")
	}
	return ScanBytes(data)
}

func looksLikePDF(data []byte) bool {
	limit := 1024
	if len(data) < limit {
		limit = len(data)
	}
	return bytes.Contains(data[:limit], []byte("%PDF-"))
}

// walk traverses every value reachable from the catalog. role describes how
// the value was reached so that catalog-specific rules can be applied.
func (s *scanner) walk(v *Value, role string, depth, owner int) {
	if v == nil || depth > maxWalkDepth {
		return
	}
	s.walkCount++
	if s.walkCount > maxObjects {
		return
	}

	if v.Kind == KindRef {
		resolved, ok := s.store.resolve(v)
		if !ok {
			return
		}
		owner = resolved.ObjNum
		key := visitKey{objNum: resolved.ObjNum, role: role}
		if s.seen[key] {
			return
		}
		s.seen[key] = true
		s.walk(resolved, role, depth+1, owner)
		return
	}

	switch v.Kind {
	case KindDict, KindStream:
		d := v.Dict
		if v.Kind == KindStream {
			d = v.Stream.Dict
		}
		if v.ObjNum != 0 {
			owner = v.ObjNum
		}
		s.checkDict(owner, d, role)
		for _, k := range d.Keys {
			s.walk(d.Map[k], roleOf(role, k), depth+1, owner)
		}
	case KindArray:
		for _, item := range v.Array {
			s.walk(item, role, depth+1, owner)
		}
	}
}

// roleOf narrows the traversal role for well-known containment keys.
func roleOf(parent, key string) string {
	switch key {
	case "Pages", "Kids":
		return "pages"
	case "Page":
		return "page"
	case "Annots":
		return "annots"
	case "AcroForm":
		return "acroform"
	case "Fields":
		return "fields"
	case "Names":
		return "names"
	case "OpenAction":
		return "openaction"
	case "AA":
		return "additional"
	case "A":
		return "action"
	default:
		return parent
	}
}

func (s *scanner) checkDict(objNum int, d *Dict, role string) {

	if role == "catalog" {
		if oa := d.Get("OpenAction"); oa != nil && oa.Kind != KindNull {
			// A destination (array) only sets the initial view and runs no
			// action; only a dictionary/action reference is executable.
			if oa.Kind == KindDict || oa.Kind == KindRef {
				s.add(objNum, FindOpenAction, "OpenAction")
				s.walk(oa, "openaction", 0, objNum)
			}
		}
	}

	if aa := d.Get("AA"); aa != nil && aa.Kind != KindNull {
		s.add(objNum, FindAdditional, "AA")
		s.walk(aa, "additional", 0, objNum)
	}

	if js := d.Get("JS"); js != nil && js.Kind != KindNull {
		s.add(objNum, FindJavaScript, "JS")
	}

	if sv := d.Get("S"); sv != nil && sv.Kind == KindName {
		switch strings.ToLower(string(sv.Str)) {
		case "launch":
			s.add(objNum, FindLaunch, "S")
		case "javascript":
			s.add(objNum, FindJavaScript, "S")
		case "submitform":
			s.add(objNum, FindSubmitForm, "S")
		}
	}
}

func (s *scanner) add(objNum int, kind FindingKind, key string) {
	id := strconv.Itoa(objNum) + "|" + string(kind) + "|" + key
	if s.dedup[id] {
		return
	}
	s.dedup[id] = true
	s.findings = append(s.findings, Finding{ObjectNum: objNum, Kind: kind, Key: key})
}
