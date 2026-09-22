package groupbreaker

import "time"

// Timer abstracts a scheduled one-shot callback so tests can drive it manually.
type Timer interface {
	Stop() bool
}

// Clock abstracts time so tests can inject a fake clock.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// RealClock is the production Clock backed by the time package.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}
