package msgproc

import (
	"errors"
	"sync"
)

var ErrTicketNotFound = errors.New("msgproc: ticket not found")

type ticketRecord struct {
	id         string
	messageID  string
	payload    []byte
	phase      Phase
	attempts   []Attempt
	result     interface{}
	resultLost bool
}

// Store 是票据与尝试历史的存储。内存实现用于演示与测试，
// 接口语义（先持久化签收再返回票据）与外部存储一致。
type Store struct {
	mu      sync.Mutex
	seq     int
	byMsgID map[string]string
	tickets map[string]*ticketRecord
}

func NewStore() *Store {
	return &Store{
		byMsgID: make(map[string]string),
		tickets: make(map[string]*ticketRecord),
	}
}

func (s *Store) getOrCreateLocked(messageID string, payload []byte) (*ticketRecord, bool) {
	if id, ok := s.byMsgID[messageID]; ok {
		return s.tickets[id], false
	}
	s.seq++
	rec := &ticketRecord{
		id:        "t-" + messageID + "-" + itoa(s.seq),
		messageID: messageID,
		payload:   payload,
		phase:     PhaseAcked,
	}
	s.byMsgID[messageID] = rec.id
	s.tickets[rec.id] = rec
	return rec, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
