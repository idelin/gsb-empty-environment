package netsec

import "fmt"

const (
	// MaxEngineBoots is the largest permitted USM engineBoots value. Once an
	// engine reaches this value it must not accept any more authenticated
	// messages.
	MaxEngineBoots int32 = 2147483647

	// TimeWindowSeconds is the inclusive SNMPv3 USM time window. A difference of
	// exactly 150 seconds is accepted; anything larger is not.
	TimeWindowSeconds int64 = 150
)

// Additional names for the USM limits.
const (
	EngineBootsLimit     = MaxEngineBoots
	MaxEngineBootsValue  = MaxEngineBoots
	USMTimeWindowSeconds = TimeWindowSeconds
)

// TimeState carries the local or message engine time values used by USM.
// EngineTime is measured in seconds.
type TimeState struct {
	EngineBoots int32
	EngineTime  int64
}

// Aliases used by different SNMP integrations.
type (
	USMTime         = TimeState
	EngineTimeState = TimeState
	EngineTimes     = TimeState
)

// LocalTime constructs a local time state.
func LocalTime(engineBoots int32, engineTime int64) TimeState {
	return TimeState{EngineBoots: engineBoots, EngineTime: engineTime}
}

// MessageTime constructs a message time state.
func MessageTime(engineBoots int32, engineTime int64) TimeState {
	return TimeState{EngineBoots: engineBoots, EngineTime: engineTime}
}

// CheckTimeWindow checks message timeliness against the caller-supplied local
// engineBoots and engineTime. It is a pure function and never updates state.
//
// A local engineBoots value of MaxEngineBoots rejects every message. A
// different message engineBoots value is also rejected. The engineTime
// comparison is inclusive: a difference of 150 seconds is accepted, while 151
// seconds is not.
func CheckTimeWindow(local TimeState, message TimeState) Report {
	if local.EngineBoots == MaxEngineBoots {
		return NotInTimeWindow
	}
	if message.EngineBoots != local.EngineBoots {
		return NotInTimeWindow
	}
	if absDifference(message.EngineTime, local.EngineTime) > TimeWindowSeconds {
		return NotInTimeWindow
	}
	return ReportOK
}

// CheckTimeWindowState is an explicitly typed alias for CheckTimeWindow.
func CheckTimeWindowState(local TimeState, message TimeState) Report {
	return CheckTimeWindow(local, message)
}

// CheckTimeWindowValues is a convenient form of CheckTimeWindow for callers
// that keep the four values separately.
func CheckTimeWindowValues(localEngineBoots int32, localEngineTime int64, messageEngineBoots int32, messageEngineTime int64) Report {
	return CheckTimeWindow(
		TimeState{EngineBoots: localEngineBoots, EngineTime: localEngineTime},
		TimeState{EngineBoots: messageEngineBoots, EngineTime: messageEngineTime},
	)
}

// ValidateTimeWindow performs the same check but rejects the message as
// AuthenticationFailed when authenticated is false. Callers should only set
// authenticated after their authentication callback has succeeded.
func ValidateTimeWindow(local TimeState, message TimeState, authenticated bool) Report {
	if !authenticated {
		return AuthenticationFailed
	}
	return CheckTimeWindow(local, message)
}

// ValidateTimeWindowValues is the value-oriented form of ValidateTimeWindow.
func ValidateTimeWindowValues(localEngineBoots int32, localEngineTime int64, messageEngineBoots int32, messageEngineTime int64, authenticated bool) Report {
	if !authenticated {
		return AuthenticationFailed
	}
	return CheckTimeWindowValues(localEngineBoots, localEngineTime, messageEngineBoots, messageEngineTime)
}

// Difference returns the absolute difference between two engineTime values
// without overflowing for arbitrary int64 inputs.
func Difference(a int64, b int64) (int64, bool) {
	if a >= b {
		if a > b && b < 0 && a > maxInt64+b {
			return 0, false
		}
		return a - b, true
	}
	if a < 0 && b > maxInt64+a {
		return 0, false
	}
	return b - a, true
}

func absDifference(a int64, b int64) int64 {
	d, ok := Difference(a, b)
	if !ok {
		// Only representable engineTime differences can be inside the window.
		return maxInt64
	}
	return d
}

const maxInt64 = int64(^uint64(0) >> 1)

// String makes TimeState easy to include in diagnostics.
func (t TimeState) String() string {
	return fmt.Sprintf("boots=%d time=%d", t.EngineBoots, t.EngineTime)
}
