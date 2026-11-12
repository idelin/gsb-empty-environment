package netsec

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrCycle is returned when a derived segment would (directly or
// transitively) depend on itself.
var ErrCycle = errors.New("netsec: cyclic dependency")

// ErrMissing matches errors reporting intervals without evidence.
var ErrMissing = errors.New("netsec: missing evidence")

// MissingError reports a read or dependency resolution that reached an
// interval with no declared evidence.
type MissingError struct {
	Span Span
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("netsec: no evidence for span [%d,%d)", e.Span.Off, e.Span.End())
}

// Is reports whether target is ErrMissing.
func (e *MissingError) Is(target error) bool { return target == ErrMissing }

type segment struct {
	Segment
}

// Image is a sparse forensic image stitched from multiple evidence
// sources. It implements io.ReaderAt; every read also yields the
// minimal set of original source fragments backing the bytes.
//
// Declarations layer on top of each other: where segments overlap, the
// most recently declared segment covers the interval. The provenance
// cache only ever holds fragments resolved from Original segments and
// is discarded on every new declaration, so an unverified (missing or
// since-overridden) resolution can never be promoted to evidence.
type Image struct {
	mu      sync.Mutex
	segs    []*segment // declaration order; later wins on overlap
	sources map[string]io.ReaderAt
	cache   map[*segment][]Fragment
}

// New returns an empty sparse image.
func New() *Image {
	return &Image{
		sources: make(map[string]io.ReaderAt),
		cache:   make(map[*segment][]Fragment),
	}
}

// AddSource registers an evidence source under name. Original segments
// refer to it by Source. Registering the same name again is an error.
func (im *Image) AddSource(name string, r io.ReaderAt) error {
	if name == "" {
		return errors.New("netsec: empty source name")
	}
	if r == nil {
		return fmt.Errorf("netsec: nil reader for source %q", name)
	}
	im.mu.Lock()
	defer im.mu.Unlock()
	if _, dup := im.sources[name]; dup {
		return fmt.Errorf("netsec: source %q already registered", name)
	}
	im.sources[name] = r
	return nil
}

// Declare layers a segment onto the image. A Derived segment whose
// dependency intervals would introduce a dependency cycle is rejected
// with ErrCycle and the image is left unchanged.
func (im *Image) Declare(seg Segment) error {
	if seg.Span.Off < 0 || seg.Span.Len <= 0 {
		return fmt.Errorf("netsec: invalid span [%d,%d)", seg.Span.Off, seg.Span.End())
	}
	switch seg.Kind {
	case Original:
		if seg.Source == "" {
			return errors.New("netsec: original segment requires a source")
		}
		if len(seg.Deps) != 0 {
			return errors.New("netsec: original segment must not declare dependencies")
		}
	case Derived:
		if seg.Source != "" {
			return errors.New("netsec: derived segment must not name a source")
		}
		if len(seg.Deps) == 0 {
			return errors.New("netsec: derived segment requires dependencies")
		}
		for _, d := range seg.Deps {
			if d.Off < 0 || d.Len <= 0 {
				return fmt.Errorf("netsec: invalid dependency span [%d,%d)", d.Off, d.End())
			}
		}
	case Missing:
		if seg.Source != "" || len(seg.Deps) != 0 {
			return errors.New("netsec: missing segment carries neither source nor dependencies")
		}
	default:
		return fmt.Errorf("netsec: unknown segment kind %d", seg.Kind)
	}

	im.mu.Lock()
	defer im.mu.Unlock()
	if seg.Kind == Original {
		if _, ok := im.sources[seg.Source]; !ok {
			return fmt.Errorf("netsec: unknown source %q", seg.Source)
		}
	}
	im.segs = append(im.segs, &segment{Segment: seg})
	if seg.Kind == Derived {
		if err := im.checkCycleLocked(); err != nil {
			im.segs = im.segs[:len(im.segs)-1]
			return err
		}
	}
	// Any new declaration may override coverage; drop all cached
	// resolutions so stale provenance is never served as evidence.
	im.cache = make(map[*segment][]Fragment)
	return nil
}

// checkCycleLocked rejects any cycle among derived segments. An edge
// s -> t exists when a dependency of s overlaps the span of t.
func (im *Image) checkCycleLocked() error {
	var derived []*segment
	for _, s := range im.segs {
		if s.Kind == Derived {
			derived = append(derived, s)
		}
	}
	const (
		white = iota
		gray
		black
	)
	color := make(map[*segment]int, len(derived))
	var visit func(s *segment) error
	visit = func(s *segment) error {
		color[s] = gray
		for _, dep := range s.Deps {
			for _, t := range derived {
				if !dep.Overlaps(t.Span) {
					continue
				}
				switch color[t] {
				case gray:
					return fmt.Errorf("%w: [%d,%d) depends on itself",
						ErrCycle, s.Span.Off, s.Span.End())
				case white:
					if err := visit(t); err != nil {
						return err
					}
				}
			}
		}
		color[s] = black
		return nil
	}
	for _, s := range derived {
		if color[s] == white {
			if err := visit(s); err != nil {
				return err
			}
		}
	}
	return nil
}

// coverLocked returns the most recently declared segment covering off.
func (im *Image) coverLocked(off int64) *segment {
	for i := len(im.segs) - 1; i >= 0; i-- {
		if s := im.segs[i]; s.Span.Off <= off && off < s.Span.End() {
			return s
		}
	}
	return nil
}

// resolveLocked computes the minimal fragment set backing span.
func (im *Image) resolveLocked(span Span, visiting map[*segment]bool) ([]Fragment, error) {
	var frags []Fragment
	pos := span.Off
	for pos < span.End() {
		s := im.coverLocked(pos)
		if s == nil {
			return nil, &MissingError{Span: Span{Off: pos, Len: span.End() - pos}}
		}
		sub := Span{Off: pos, Len: min(s.Span.End(), span.End()) - pos}
		switch s.Kind {
		case Missing:
			return nil, &MissingError{Span: sub}
		case Original:
			frags = append(frags, Fragment{
				Source: s.Source,
				Span:   Span{Off: s.SrcOff + (sub.Off - s.Span.Off), Len: sub.Len},
			})
		case Derived:
			if visiting[s] {
				return nil, fmt.Errorf("%w: [%d,%d)", ErrCycle, s.Span.Off, s.Span.End())
			}
			resolved, ok := im.cache[s]
			if !ok {
				visiting[s] = true
				var depFrags []Fragment
				for _, dep := range s.Deps {
					f, err := im.resolveLocked(dep, visiting)
					if err != nil {
						return nil, err
					}
					depFrags = append(depFrags, f...)
				}
				delete(visiting, s)
				// Only fully verified resolutions of Original
				// fragments reach the cache; failures above leave
				// no trace behind.
				resolved = MinimizeFragments(depFrags)
				im.cache[s] = resolved
			}
			frags = append(frags, resolved...)
		}
		pos = sub.End()
	}
	return MinimizeFragments(frags), nil
}

// ReadWithProvenance reads len(p) bytes at off and returns the minimal
// set of original source fragments backing them. It fails with a
// *MissingError (matching ErrMissing) if any byte lacks evidence, and
// with ErrCycle if dependency resolution loops.
func (im *Image) ReadWithProvenance(p []byte, off int64) (int, []Fragment, error) {
	if off < 0 {
		return 0, nil, fmt.Errorf("netsec: negative offset %d", off)
	}
	if len(p) == 0 {
		return 0, nil, nil
	}
	im.mu.Lock()
	defer im.mu.Unlock()

	span := Span{Off: off, Len: int64(len(p))}
	frags, err := im.resolveLocked(span, make(map[*segment]bool))
	if err != nil {
		return 0, nil, err
	}

	pos := off
	for pos < span.End() {
		s := im.coverLocked(pos) // resolveLocked proved coverage
		n := min(s.Span.End(), span.End()) - pos
		buf := p[pos-off : pos-off+n : pos-off+n]
		srcOff := s.SrcOff + (pos - s.Span.Off)
		var r io.ReaderAt
		switch s.Kind {
		case Original:
			r = im.sources[s.Source]
		case Derived:
			r = s.Data
		}
		if r == nil {
			return 0, nil, fmt.Errorf("netsec: no byte reader for span [%d,%d)", s.Span.Off, s.Span.End())
		}
		if _, err := r.ReadAt(buf, srcOff); err != nil {
			return 0, nil, fmt.Errorf("netsec: reading span [%d,%d): %w", pos, pos+n, err)
		}
		pos += n
	}
	return len(p), frags, nil
}

// ReadAt implements io.ReaderAt. A read either returns len(p) bytes or
// an error carrying the evidence gap; it never fabricates zero bytes.
func (im *Image) ReadAt(p []byte, off int64) (int, error) {
	n, _, err := im.ReadWithProvenance(p, off)
	return n, err
}

// Provenance returns the minimal fragment set backing [off, off+length)
// without reading any bytes.
func (im *Image) Provenance(off, length int64) ([]Fragment, error) {
	if off < 0 || length < 0 {
		return nil, fmt.Errorf("netsec: invalid span [%d,%d)", off, off+length)
	}
	if length == 0 {
		return nil, nil
	}
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.resolveLocked(Span{Off: off, Len: length}, make(map[*segment]bool))
}
