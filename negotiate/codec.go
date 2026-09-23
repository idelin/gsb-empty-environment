package negotiate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"negproto/wire"
)

// Canonical encoding tags. Only these tags are decodable; anything else is
// rejected, so decoding can never be coerced into interpreting unexpected
// structures.
const (
	tagProfile     = "negproto/profile/v1"
	tagCertificate = "negproto/certificate/v1"
	tagSummary     = "negproto/summary/v1"
)

func encodeProfile(e *wire.Encoder, p *Profile) {
	e.String(tagProfile)
	e.String(p.Name)
	feats := append([]FeatureDecl(nil), p.Features...)
	sort.Slice(feats, func(i, j int) bool { return feats[i].Name < feats[j].Name })
	e.Uvarint(uint64(len(feats)))
	for _, f := range feats {
		e.String(f.Name)
		e.Uvarint(uint64(f.Versions.Min))
		e.Uvarint(uint64(f.Versions.Max))
		e.Bool(f.Required)
		e.Varint(int64(f.Priority))
		deps := append([]Requirement(nil), f.Depends...)
		sort.Slice(deps, func(i, j int) bool {
			if deps[i].Feature != deps[j].Feature {
				return deps[i].Feature < deps[j].Feature
			}
			if deps[i].Range.Min != deps[j].Range.Min {
				return deps[i].Range.Min < deps[j].Range.Min
			}
			return deps[i].Range.Max < deps[j].Range.Max
		})
		e.Uvarint(uint64(len(deps)))
		for _, d := range deps {
			e.String(d.Feature)
			e.Uvarint(uint64(d.Range.Min))
			e.Uvarint(uint64(d.Range.Max))
			e.Bool(d.When != nil)
			if d.When != nil {
				e.Uvarint(uint64(d.When.Min))
				e.Uvarint(uint64(d.When.Max))
			}
		}
		confs := append([]string(nil), f.Conflicts...)
		sort.Strings(confs)
		e.Uvarint(uint64(len(confs)))
		for _, c := range confs {
			e.String(c)
		}
	}
}

func decodeProfile(d *wire.Decoder) (*Profile, error) {
	if err := d.Enter(); err != nil {
		return nil, err
	}
	defer d.Leave()
	tag, err := d.String()
	if err != nil {
		return nil, err
	}
	if tag != tagProfile {
		return nil, fmt.Errorf("negotiate: unexpected profile tag %q", tag)
	}
	name, err := d.String()
	if err != nil {
		return nil, err
	}
	n, err := d.Count(wire.MaxCount)
	if err != nil {
		return nil, err
	}
	p := &Profile{Name: name, Features: make([]FeatureDecl, 0, n)}
	for i := 0; i < n; i++ {
		f, err := decodeFeature(d)
		if err != nil {
			return nil, err
		}
		p.Features = append(p.Features, f)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func decodeFeature(d *wire.Decoder) (FeatureDecl, error) {
	var f FeatureDecl
	var err error
	if f.Name, err = d.String(); err != nil {
		return f, err
	}
	if f.Versions.Min, err = uint(d.Uvarint()); err != nil {
		return f, err
	}
	if f.Versions.Max, err = uint(d.Uvarint()); err != nil {
		return f, err
	}
	if f.Required, err = d.Bool(); err != nil {
		return f, err
	}
	prio, err := d.Varint()
	if err != nil {
		return f, err
	}
	f.Priority = int(prio)
	nd, err := d.Count(wire.MaxCount)
	if err != nil {
		return f, err
	}
	f.Depends = make([]Requirement, 0, nd)
	for i := 0; i < nd; i++ {
		var r Requirement
		if r.Feature, err = d.String(); err != nil {
			return f, err
		}
		if r.Range.Min, err = uint(d.Uvarint()); err != nil {
			return f, err
		}
		if r.Range.Max, err = uint(d.Uvarint()); err != nil {
			return f, err
		}
		hasWhen, err := d.Bool()
		if err != nil {
			return f, err
		}
		if hasWhen {
			var w VersionRange
			if w.Min, err = uint(d.Uvarint()); err != nil {
				return f, err
			}
			if w.Max, err = uint(d.Uvarint()); err != nil {
				return f, err
			}
			r.When = &w
		}
		f.Depends = append(f.Depends, r)
	}
	nc, err := d.Count(wire.MaxCount)
	if err != nil {
		return f, err
	}
	f.Conflicts = make([]string, 0, nc)
	for i := 0; i < nc; i++ {
		c, err := d.String()
		if err != nil {
			return f, err
		}
		f.Conflicts = append(f.Conflicts, c)
	}
	return f, nil
}

// uint converts a decoded uvarint to int with an explicit bound check.
func uint(v uint64, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	if v > uint64(wire.MaxCount) {
		return 0, wire.ErrLength
	}
	return int(v), nil
}

func encodeSelection(e *wire.Encoder, sel Selection) {
	names := make([]string, 0, len(sel))
	for n := range sel {
		names = append(names, n)
	}
	sort.Strings(names)
	e.Uvarint(uint64(len(names)))
	for _, n := range names {
		e.String(n)
		e.Uvarint(uint64(sel[n]))
	}
}

func decodeSelection(d *wire.Decoder) (Selection, error) {
	n, err := d.Count(wire.MaxCount)
	if err != nil {
		return nil, err
	}
	sel := make(Selection, n)
	for i := 0; i < n; i++ {
		name, err := d.String()
		if err != nil {
			return nil, err
		}
		v, err := uint(d.Uvarint())
		if err != nil {
			return nil, err
		}
		if _, dup := sel[name]; dup {
			return nil, fmt.Errorf("negotiate: duplicate selection entry %q", name)
		}
		sel[name] = v
	}
	return sel, nil
}

// SummaryOf computes the session summary that binds both profiles and the
// negotiated selection. All later decoding for this session must present a
// certificate carrying this exact summary.
func SummaryOf(a, b *Profile, sel Selection) [32]byte {
	var e wire.Encoder
	e.String(tagSummary)
	encodeProfile(&e, a)
	encodeProfile(&e, b)
	encodeSelection(&e, sel)
	return sha256.Sum256(e.Out())
}

// MarshalProfile encodes a profile in canonical form.
func MarshalProfile(p *Profile) []byte {
	var e wire.Encoder
	encodeProfile(&e, p)
	return e.Out()
}

// UnmarshalProfile decodes a profile, rejecting trailing garbage.
func UnmarshalProfile(data []byte) (*Profile, error) {
	d := wire.NewDecoder(data)
	p, err := decodeProfile(d)
	if err != nil {
		return nil, err
	}
	if err := d.Done(); err != nil {
		return nil, err
	}
	return p, nil
}

// ErrSummaryMismatch is returned when a certificate's summary does not
// match the summary the current session expects.
var ErrSummaryMismatch = errors.New("negotiate: certificate summary does not match session binding")

// Marshal encodes the certificate in canonical form.
func (c Certificate) Marshal() []byte {
	var e wire.Encoder
	e.String(tagCertificate)
	encodeSelection(&e, c.Selection)
	e.Bytes(c.Summary[:])
	return e.Out()
}

// UnmarshalCertificate decodes a certificate and requires it to carry
// expectSummary. An old certificate replayed against renegotiated
// parameters fails here.
func UnmarshalCertificate(data []byte, expectSummary [32]byte) (Certificate, error) {
	var c Certificate
	d := wire.NewDecoder(data)
	if err := d.Enter(); err != nil {
		return c, err
	}
	tag, err := d.String()
	if err != nil {
		return c, err
	}
	if tag != tagCertificate {
		return c, fmt.Errorf("negotiate: unexpected certificate tag %q", tag)
	}
	if c.Selection, err = decodeSelection(d); err != nil {
		return c, err
	}
	sum, err := d.Bytes()
	if err != nil {
		return c, err
	}
	if len(sum) != 32 {
		return c, fmt.Errorf("negotiate: bad summary length %d", len(sum))
	}
	copy(c.Summary[:], sum)
	if err := d.Done(); err != nil {
		return c, err
	}
	if c.Summary != expectSummary {
		return c, ErrSummaryMismatch
	}
	return c, nil
}
