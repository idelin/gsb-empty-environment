package main

import "sync"

// Op 是变更事件的操作类型。
type Op int

const (
	OpPut Op = iota
	OpDelete
)

func (o Op) String() string {
	if o == OpDelete {
		return "DEL"
	}
	return "PUT"
}

// Event 是一条变更事件。Version 由事件日志全局单调递增分配。
// 事件可能被重发或乱序投递，消费方必须按 (Key, Version) 消重。
type Event struct {
	Key     string
	Version uint64
	Op      Op
	Value   string
}

// EventLog 是仅追加的事件日志，版本号从 1 开始连续分配。
type EventLog struct {
	mu     sync.Mutex
	events []Event // events[i].Version == i+1
}

func NewEventLog() *EventLog {
	return &EventLog{}
}

// Append 写入一条变更并返回分配好版本号的事件。
func (l *EventLog) Append(key string, op Op, value string) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := Event{Key: key, Version: uint64(len(l.events)) + 1, Op: op, Value: value}
	l.events = append(l.events, e)
	return e
}

// MaxVersion 返回当前日志的最大版本号（冻结版本据此确定）。
func (l *EventLog) MaxVersion() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return uint64(len(l.events))
}

// Since 返回所有 Version > since 的事件（按版本升序的副本）。
func (l *EventLog) Since(since uint64) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if since >= uint64(len(l.events)) {
		return nil
	}
	out := make([]Event, len(l.events)-int(since))
	copy(out, l.events[since:])
	return out
}
