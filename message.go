package main

import "fmt"

type MsgType int

const (
	MsgVoteRequest MsgType = iota
	MsgVoteResponse
	MsgAppend
	MsgAppendAck
)

func (t MsgType) String() string {
	switch t {
	case MsgVoteRequest:
		return "VoteRequest"
	case MsgVoteResponse:
		return "VoteResponse"
	case MsgAppend:
		return "Append"
	case MsgAppendAck:
		return "AppendAck"
	}
	return "?"
}

// Entry is one log record. Cfg is non-nil for configuration entries.
type Entry struct {
	Index int
	View  int
	Cmd   string
	Cfg   *Config
}

// Message is the only unit of communication. Every message carries the
// sender's view number; receivers reject anything from an older view.
type Message struct {
	Type MsgType
	View int
	From int
	To   int

	// vote request / response
	Granted   bool
	LastIndex int
	LastView  int

	// append
	PrevIndex    int
	PrevView     int
	Entries      []Entry
	LeaderCommit int

	// append ack
	Accept   bool
	AckIndex int
}

func (m Message) String() string {
	base := fmt.Sprintf("%s v%d %d->%d", m.Type, m.View, m.From, m.To)
	switch m.Type {
	case MsgVoteResponse:
		return fmt.Sprintf("%s granted=%v", base, m.Granted)
	case MsgAppend:
		return fmt.Sprintf("%s prev=(%d,v%d) entries=%d commit=%d", base, m.PrevIndex, m.PrevView, len(m.Entries), m.LeaderCommit)
	case MsgAppendAck:
		return fmt.Sprintf("%s accept=%v ack=%d", base, m.Accept, m.AckIndex)
	}
	return base
}
