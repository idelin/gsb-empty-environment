package objstore

import "time"

// Clock 抽象时间源，便于用虚拟时钟确定性地重放边界竞争。
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer 是一次性定时任务。
type Timer interface {
	Stop() bool
}

type realClock struct{}

// RealClock 返回基于标准库 time 包的真实时钟。
func RealClock() Clock { return realClock{} }

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}
