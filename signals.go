package takeover

import (
	"encoding/json"
	"sort"
)

// Signal is a portable signal name. The package does not import os/signal so
// that grant values can be serialized and checked on either side of a proxy.
type Signal string

const (
	SignalInterrupt Signal = "SIGINT"
	SignalTerminate Signal = "SIGTERM"
	SignalQuit      Signal = "SIGQUIT"
	SignalHangup    Signal = "SIGHUP"
	SignalKill      Signal = "SIGKILL"
	SignalStop      Signal = "SIGSTOP"
	SignalContinue  Signal = "SIGCONT"
	SignalResize    Signal = "SIGWINCH"
)

// SignalSet is a set of allowed signals. A nil set is empty.
type SignalSet map[Signal]struct{}

// NewSignalSet constructs a set from signal names.
func NewSignalSet(signals ...Signal) SignalSet {
	set := make(SignalSet, len(signals))
	for _, signal := range signals {
		if signal != "" {
			set[signal] = struct{}{}
		}
	}
	return set
}

// Signals is a convenience alias for NewSignalSet.
func Signals(signals ...Signal) SignalSet {
	return NewSignalSet(signals...)
}

// Clone returns an independent copy.
func (s SignalSet) Clone() SignalSet {
	if s == nil {
		return nil
	}
	out := make(SignalSet, len(s))
	for signal := range s {
		out[signal] = struct{}{}
	}
	return out
}

// Contains reports whether signal is allowed.
func (s SignalSet) Contains(signal Signal) bool {
	_, ok := s[signal]
	return ok
}

// IsEmpty reports whether the set has no members.
func (s SignalSet) IsEmpty() bool {
	return len(s) == 0
}

// Equal reports set equality.
func (s SignalSet) Equal(other SignalSet) bool {
	if len(s) != len(other) {
		return false
	}
	for signal := range s {
		if _, ok := other[signal]; !ok {
			return false
		}
	}
	return true
}

// SubsetOf reports whether every signal in s is in parent.
func (s SignalSet) SubsetOf(parent SignalSet) bool {
	for signal := range s {
		if _, ok := parent[signal]; !ok {
			return false
		}
	}
	return true
}

// Union returns the union of s and other.
func (s SignalSet) Union(other SignalSet) SignalSet {
	out := s.Clone()
	if out == nil {
		out = make(SignalSet)
	}
	for signal := range other {
		out[signal] = struct{}{}
	}
	return out
}

// List returns the members in stable order.
func (s SignalSet) List() []Signal {
	out := make([]Signal, 0, len(s))
	for signal := range s {
		out = append(out, signal)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MarshalJSON encodes signals as a stable array.
func (s SignalSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.List())
}

// UnmarshalJSON decodes an array of signal names.
func (s *SignalSet) UnmarshalJSON(data []byte) error {
	var signals []Signal
	if err := json.Unmarshal(data, &signals); err != nil {
		return err
	}
	*s = NewSignalSet(signals...)
	return nil
}
