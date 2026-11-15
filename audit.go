package netsec

import (
	"fmt"
	"sync"
)

type EventKind int

const (
	EvOutput EventKind = iota
	EvDeclassify
	EvRollback
)

// Event 是一条审计记录。
type Event struct {
	Kind    EventKind
	Request string
	Node    string
	Removed Label
	Detail  string
}

func (ev Event) String() string {
	switch ev.Kind {
	case EvOutput:
		return fmt.Sprintf("OUTPUT req=%s", ev.Request)
	case EvDeclassify:
		return fmt.Sprintf("DECLASSIFY req=%s node=%s removed=%04x", ev.Request, ev.Node, uint16(ev.Removed))
	case EvRollback:
		return fmt.Sprintf("ROLLBACK req=%s err=%s", ev.Request, ev.Detail)
	}
	return "UNKNOWN"
}

// AuditLog 是并发安全的事件审计日志。
type AuditLog struct {
	mu     sync.Mutex
	events []Event
}

func NewAuditLog() *AuditLog { return &AuditLog{} }

func (l *AuditLog) Log(ev Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *AuditLog) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

func (l *AuditLog) Count(kind EventKind) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, ev := range l.events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}
