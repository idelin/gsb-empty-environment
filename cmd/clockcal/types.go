package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"

	"clockcal/internal/calib"
)

// Number accepts a JSON number or a string holding an integer, a decimal, or
// a rational "p/q".
type Number struct {
	raw string
}

func (n *Number) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return fmt.Errorf("empty number")
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		n.raw = s
	} else {
		n.raw = string(data)
	}
	if _, err := n.rat(); err != nil {
		return err
	}
	return nil
}

func (n Number) rat() (*big.Rat, error) { return calib.ParseRat(n.raw) }

// Bounds is an optional [min, max] pair; either side may be null.
type Bounds struct {
	Min *Number `json:"min"`
	Max *Number `json:"max"`
}

func (b *Bounds) interval() (calib.Interval, error) {
	var iv calib.Interval
	if b == nil {
		return iv, nil
	}
	if b.Min != nil {
		r, err := b.Min.rat()
		if err != nil {
			return iv, fmt.Errorf("bound min: %v", err)
		}
		iv.Lo = r
	}
	if b.Max != nil {
		r, err := b.Max.rat()
		if err != nil {
			return iv, fmt.Errorf("bound max: %v", err)
		}
		iv.Hi = r
	}
	return iv, nil
}

// CalibrationPoint states that at local time Local the true global time lies
// in [Global.Min, Global.Max].
type CalibrationPoint struct {
	ID     string `json:"id"`
	Local  Number `json:"local"`
	Global Bounds `json:"global"`
}

type EventIn struct {
	ID    string `json:"id"`
	Local Number `json:"local"`
}

type Channel struct {
	Name        string             `json:"name"`
	DriftBounds *Bounds            `json:"drift_bounds"`
	Calibration []CalibrationPoint `json:"calibration"`
	Events      []EventIn          `json:"events"`
}

type Input struct {
	Channels []Channel `json:"channels"`
}

func (ch *Channel) constraints() ([]calib.Constraint, error) {
	cs := make([]calib.Constraint, 0, len(ch.Calibration))
	for i, p := range ch.Calibration {
		id := p.ID
		if id == "" {
			id = fmt.Sprintf("%s:c%d", ch.Name, i)
		}
		t, err := p.Local.rat()
		if err != nil {
			return nil, fmt.Errorf("calibration %q: local: %v", id, err)
		}
		iv, err := p.Global.interval()
		if err != nil {
			return nil, fmt.Errorf("calibration %q: %v", id, err)
		}
		if iv.Lo == nil || iv.Hi == nil {
			return nil, fmt.Errorf("calibration %q: global interval must be bounded", id)
		}
		if iv.Lo.Cmp(iv.Hi) > 0 {
			return nil, fmt.Errorf("calibration %q: empty global interval", id)
		}
		cs = append(cs, calib.Constraint{ID: id, T: t, Lo: iv.Lo, Hi: iv.Hi})
	}
	return cs, nil
}

// IntervalJSON renders an exact interval; null means unbounded.
type IntervalJSON struct {
	Min any `json:"min"`
	Max any `json:"max"`
}

func newIntervalJSON(iv calib.Interval) IntervalJSON {
	var lo, hi any
	if iv.Lo != nil {
		lo = iv.Lo.RatString()
	}
	if iv.Hi != nil {
		hi = iv.Hi.RatString()
	}
	return IntervalJSON{Min: lo, Max: hi}
}

type ChannelReport struct {
	Name         string              `json:"name"`
	Status       string              `json:"status"`
	Drift        IntervalJSON        `json:"drift_rate"`
	Offset       IntervalJSON        `json:"offset"`
	OffsetIntMin any                 `json:"offset_integer_min"`
	OffsetIntMax any                 `json:"offset_integer_max"`
	IntFeas      bool                `json:"integer_offset_feasible"`
	Used         []string            `json:"compatible_calibration"`
	Dropped      []CalibrationReport `json:"incompatible_calibration"`
}

type CalibrationReport struct {
	ID     string       `json:"id"`
	Local  string       `json:"local"`
	Global IntervalJSON `json:"global"`
}

type EventReport struct {
	ID      string       `json:"id"`
	Channel string       `json:"channel"`
	Local   string       `json:"local"`
	Global  IntervalJSON `json:"global_interval"`
}

type Edge struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type PartialOrderReport struct {
	Edges        []Edge      `json:"edges"`
	Incomparable [][2]string `json:"incomparable"`
}

type Report struct {
	Channels     []ChannelReport    `json:"channels"`
	Events       []EventReport      `json:"events"`
	PartialOrder PartialOrderReport `json:"partial_order"`
}

func constraintIDs(cs []calib.Constraint) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

func droppedReports(cs []calib.Constraint) []CalibrationReport {
	out := make([]CalibrationReport, len(cs))
	for i, c := range cs {
		out[i] = CalibrationReport{
			ID:     c.ID,
			Local:  c.T.RatString(),
			Global: newIntervalJSON(calib.Interval{Lo: c.Lo, Hi: c.Hi}),
		}
	}
	return out
}

func intStr(v *big.Int) any {
	if v == nil {
		return nil
	}
	return v.String()
}
