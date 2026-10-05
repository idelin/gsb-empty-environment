package calib

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Rat is a JSON-friendly exact rational. It accepts a JSON string
// ("3/2", "-7", "1.5") or a JSON number, and marshals as a string in
// "p/q" (or integer) form so no precision is ever lost.
type Rat struct {
	V *big.Rat
}

func (r *Rat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	s = strings.Trim(s, "\"")
	if s == "" {
		return fmt.Errorf("empty rational")
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("invalid rational %q", s)
	}
	r.V = v
	return nil
}

func (r Rat) MarshalJSON() ([]byte, error) {
	if r.V == nil {
		return []byte("null"), nil
	}
	return []byte(strconv.Quote(r.V.RatString())), nil
}

type pointJSON struct {
	ID  string `json:"id"`
	Ref Rat    `json:"ref"`
	Lo  Rat    `json:"lo"`
	Hi  Rat    `json:"hi"`
}

type channelJSON struct {
	Name   string      `json:"name"`
	Points []pointJSON `json:"points"`
}

type eventJSON struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Reading Rat    `json:"reading"`
}

type inputJSON struct {
	Channels []channelJSON `json:"channels"`
	Events   []eventJSON   `json:"events"`
}

// DecodeInput parses the JSON problem instance.
func DecodeInput(b []byte) (Input, error) {
	var raw inputJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return Input{}, err
	}
	in := Input{}
	for _, ch := range raw.Channels {
		c := Channel{Name: ch.Name}
		for _, p := range ch.Points {
			if p.Ref.V == nil || p.Lo.V == nil || p.Hi.V == nil {
				return Input{}, fmt.Errorf("channel %q point %q: ref/lo/hi are required", ch.Name, p.ID)
			}
			c.Points = append(c.Points, Point{ID: p.ID, Ref: p.Ref.V, Lo: p.Lo.V, Hi: p.Hi.V})
		}
		in.Channels = append(in.Channels, c)
	}
	for _, e := range raw.Events {
		if e.Reading.V == nil {
			return Input{}, fmt.Errorf("event %q: reading is required", e.ID)
		}
		in.Events = append(in.Events, Event{ID: e.ID, Channel: e.Channel, Reading: e.Reading.V})
	}
	return in, nil
}

// ChannelReport is the per-channel calibration outcome.
type ChannelReport struct {
	Name               string   `json:"name"`
	Feasible           bool     `json:"feasible"`
	OffsetMin          string   `json:"offset_min,omitempty"`
	OffsetMax          string   `json:"offset_max,omitempty"`
	DriftMin           string   `json:"drift_min,omitempty"`
	DriftMax           string   `json:"drift_max,omitempty"`
	IncompatiblePoints []string `json:"incompatible_points,omitempty"`
	Note               string   `json:"note,omitempty"`
}

// EventReport is the aligned reference-time window of one event.
type EventReport struct {
	ID        string `json:"id"`
	Channel   string `json:"channel"`
	Alignable bool   `json:"alignable"`
	TimeMin   string `json:"time_min,omitempty"`
	TimeMax   string `json:"time_max,omitempty"`
	Note      string `json:"note,omitempty"`
}

// PairReport names one event pair; in "precedence" first strictly precedes
// second, in "incomparable" the order is undetermined.
type PairReport struct {
	First  string `json:"first"`
	Second string `json:"second"`
}

// Report is the full analysis result.
type Report struct {
	Channels     []ChannelReport `json:"channels"`
	Events       []EventReport   `json:"events"`
	Precedence   []PairReport    `json:"precedence"`
	Incomparable []PairReport    `json:"incomparable"`
}

func intStr(v *big.Int) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func ratStr(v *big.Rat) string {
	if v == nil {
		return ""
	}
	return v.RatString()
}

// Analyze calibrates every channel and aligns every event, keeping the
// partial order between events whose relative order is not determined.
func Analyze(in Input) Report {
	rep := Report{}
	calibs := make(map[string]Calib)
	for _, ch := range in.Channels {
		c := Calibrate(ch.Points)
		calibs[ch.Name] = c
		cr := ChannelReport{
			Name:               ch.Name,
			Feasible:           c.Feasible,
			OffsetMin:          intStr(c.OffsetLo),
			OffsetMax:          intStr(c.OffsetHi),
			DriftMin:           ratStr(c.DriftLo),
			DriftMax:           ratStr(c.DriftHi),
			IncompatiblePoints: c.Incompatible,
		}
		if c.Feasible {
			if c.OffsetLo == nil || c.OffsetHi == nil {
				cr.Note = "offset unbounded: add a calibration point at ref=0"
			} else if c.DriftLo == nil || c.DriftHi == nil {
				cr.Note = "drift unbounded: add calibration points at distinct positive ref times"
			}
		} else {
			cr.Note = "no integer offset admits a rational drift through all intervals"
		}
		rep.Channels = append(rep.Channels, cr)
	}

	for _, e := range in.Events {
		er := EventReport{ID: e.ID, Channel: e.Channel}
		c, found := calibs[e.Channel]
		if !found {
			er.Note = "unknown channel"
		} else if w, ok := EventWindow(e.Reading, c); ok {
			er.Alignable = true
			er.TimeMin = ratStr(w.Lo)
			er.TimeMax = ratStr(w.Hi)
		} else {
			er.Note = "channel not alignable (infeasible, unbounded, or non-positive drift)"
		}
		rep.Events = append(rep.Events, er)
	}

	prec, inc := PartialOrder(in.Events, calibs)
	for _, p := range prec {
		rep.Precedence = append(rep.Precedence, PairReport{First: p.First, Second: p.Second})
	}
	for _, p := range inc {
		rep.Incomparable = append(rep.Incomparable, PairReport{First: p.First, Second: p.Second})
	}
	return rep
}
