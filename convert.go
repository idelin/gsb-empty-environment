package bridge

import (
	"fmt"
	"sort"
	"strings"
)

// Frame is a decoded protocol frame. Epoch records which plan generation it
// belongs to.
type Frame struct {
	Version int
	Epoch   uint64
	Fields  []Field
}

func (f *Frame) field(id uint32) (Field, bool) {
	for _, fl := range f.Fields {
		if fl.ID == id {
			return fl, true
		}
	}
	return Field{}, false
}

type Drop struct {
	ID     uint32
	Reason string
}

type Conflict struct {
	ID     uint32
	Detail string
}

// Report describes what a single conversion did.
type Report struct {
	Epoch     uint64
	Direction string
	Copied    []uint32
	Extended  []uint32
	Restored  []uint32
	Injected  []uint32
	Dropped   []Drop
	Conflicts []Conflict
	Unknown   int // unknown fields preserved verbatim
}

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "epoch=%d %s copied=%v extended=%v restored=%v injected=%v unknown=%d",
		r.Epoch, r.Direction, r.Copied, r.Extended, r.Restored, r.Injected, r.Unknown)
	for _, d := range r.Dropped {
		fmt.Fprintf(&b, " dropped=%d(%s)", d.ID, d.Reason)
	}
	for _, c := range r.Conflicts {
		fmt.Fprintf(&b, " conflict=%d(%s)", c.ID, c.Detail)
	}
	return b.String()
}

// Convert transforms src into the target version. It is atomic: on any
// error it returns nil and no partially converted frame is observable.
func (p *Plan) Convert(src *Frame, epoch uint64, lim Limits) (*Frame, *Report, error) {
	if src.Version != p.From {
		return nil, nil, fmt.Errorf("bridge: plan v%d->v%d cannot convert a v%d frame", p.From, p.To, src.Version)
	}
	rep := &Report{Epoch: epoch, Direction: fmt.Sprintf("v%d->v%d", p.From, p.To)}
	out := &Frame{Version: p.To, Epoch: epoch}
	emitted := map[uint32]bool{}
	var extBuf []byte

	emit := func(f Field) { out.Fields = append(out.Fields, f); emitted[f.ID] = true }

	for _, f := range src.Fields {
		if p.hasSrcExt && f.ID == p.srcExtID {
			continue // carrier processed after real fields so conflicts resolve correctly
		}
		step, known := p.stepByID[f.ID]
		if !known {
			if p.hasExt {
				extBuf = f.appendWire(extBuf)
				rep.Unknown++
			} else {
				rep.Dropped = append(rep.Dropped, Drop{ID: f.ID, Reason: "unknown field, no extension carrier"})
			}
			continue
		}
		def, _ := p.to.field(f.ID)
		switch step.Action {
		case ActCopy:
			if f.Kind != def.Type.kind() {
				return nil, nil, fmt.Errorf("%w: field %d kind mismatch for schema type %s", ErrBadData, f.ID, def.Type)
			}
			emit(f)
			rep.Copied = append(rep.Copied, f.ID)
		case ActExtend:
			extBuf = f.appendWire(extBuf)
			rep.Extended = append(rep.Extended, f.ID)
		case ActDrop:
			rep.Dropped = append(rep.Dropped, Drop{ID: f.ID, Reason: step.Reason})
		}
	}

	// Restore entries carried by the source extension field.
	if p.hasSrcExt {
		if carrier, ok := src.field(p.srcExtID); ok {
			entries, err := parseFields(carrier.Raw, lim)
			if err != nil {
				return nil, nil, fmt.Errorf("bridge: corrupt extension carrier: %w", err)
			}
			keep := func(e Field, reason string) {
				if p.hasExt {
					extBuf = e.appendWire(extBuf)
				} else {
					rep.Dropped = append(rep.Dropped, Drop{ID: e.ID, Reason: reason})
				}
			}
			for _, e := range entries {
				g, known := p.to.field(e.ID)
				switch {
				case known && !g.Extension && e.Kind == g.Type.kind():
					if emitted[e.ID] {
						rep.Conflicts = append(rep.Conflicts, Conflict{ID: e.ID,
							Detail: "extension entry shadows real field; real field wins"})
						keep(e, "conflicting extension entry")
						continue
					}
					emit(e)
					rep.Restored = append(rep.Restored, e.ID)
				case !known:
					// Unknown on both sides: hand the raw field back to the
					// target frame verbatim instead of nesting it in a carrier.
					if emitted[e.ID] {
						rep.Conflicts = append(rep.Conflicts, Conflict{ID: e.ID,
							Detail: "duplicate unknown field; first occurrence wins"})
						keep(e, "conflicting extension entry")
						continue
					}
					emit(e)
					rep.Unknown++
				default:
					// Known but the wire kind no longer matches the schema:
					// keep it preserved rather than emit a schema-invalid field.
					keep(e, "extension entry unrepresentable in target")
				}
			}
		}
	}

	for _, inj := range p.Injects {
		if !emitted[inj.TargetID] {
			emit(inj.Field)
			rep.Injected = append(rep.Injected, inj.TargetID)
		}
	}
	for _, g := range p.to.Fields {
		if !g.Extension && g.Required && !emitted[g.ID] {
			return nil, nil, fmt.Errorf("bridge: conversion failed: required field %d (%s) missing", g.ID, g.Name)
		}
	}
	if len(extBuf) > 0 {
		if !p.hasExt {
			return nil, nil, fmt.Errorf("bridge: %d preserved bytes have no extension carrier", len(extBuf))
		}
		out.Fields = append(out.Fields, Field{ID: p.extID, Kind: KindBytes, Raw: extBuf})
	}
	return out, rep, nil
}

// DiffEntry is one difference between two frames.
type DiffEntry struct {
	FieldID uint32
	What    string // "missing", "extra", "value"
	Detail  string
}

// Diff compares two frames field-by-field, keyed by field ID.
func Diff(a, b *Frame) []DiffEntry {
	index := func(f *Frame) map[uint32][]Field {
		m := map[uint32][]Field{}
		for _, fl := range f.Fields {
			m[fl.ID] = append(m[fl.ID], fl)
		}
		return m
	}
	ma, mb := index(a), index(b)
	ids := map[uint32]bool{}
	for id := range ma {
		ids[id] = true
	}
	for id := range mb {
		ids[id] = true
	}
	sorted := make([]int, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, int(id))
	}
	sort.Ints(sorted)
	var out []DiffEntry
	for _, i := range sorted {
		id := uint32(i)
		fa, fb := ma[id], mb[id]
		switch {
		case len(fa) > 0 && len(fb) == 0:
			out = append(out, DiffEntry{id, "missing", "present in first frame only"})
		case len(fb) > 0 && len(fa) == 0:
			out = append(out, DiffEntry{id, "extra", "present in second frame only"})
		case len(fa) != len(fb):
			out = append(out, DiffEntry{id, "value", fmt.Sprintf("occurrence count %d != %d", len(fa), len(fb))})
		default:
			for k := range fa {
				if !fieldEqual(fa[k], fb[k]) {
					out = append(out, DiffEntry{id, "value", "field value differs"})
				}
			}
		}
	}
	return out
}

func fieldEqual(a, b Field) bool {
	if a.Kind != b.Kind || a.Num != b.Num {
		return false
	}
	if len(a.Raw) != len(b.Raw) {
		return false
	}
	for i := range a.Raw {
		if a.Raw[i] != b.Raw[i] {
			return false
		}
	}
	return true
}

// FormatDiff renders a diff report; empty diff yields "identical".
func FormatDiff(d []DiffEntry) string {
	if len(d) == 0 {
		return "identical"
	}
	var b strings.Builder
	for _, e := range d {
		fmt.Fprintf(&b, "field %d %s: %s\n", e.FieldID, e.What, e.Detail)
	}
	return b.String()
}
