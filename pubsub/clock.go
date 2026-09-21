package pubsub

import "time"

// Timer 是 time.Timer 的可注入抽象。
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// Clock 抽象时间来源，便于测试中以确定性假时钟驱动退避重试。
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// realTimer 适配标准库 time.Timer。
type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time        { return r.t.C }
func (r realTimer) Stop() bool                 { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

// realClock 使用系统墙钟。
type realClock struct{}

func (realClock) Now() time.Time                 { return time.Now() }
func (realClock) NewTimer(d time.Duration) Timer { return realTimer{t: time.NewTimer(d)} }
