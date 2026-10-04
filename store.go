package msgproc

import "sync"

// record 是票据对应的内部持久化记录。
type record struct {
	ticket   Ticket
	payload  []byte
	receipt  ReceiptState
	business BusinessState
	result   []byte
	attempts []Attempt
}

// Store 保存签收与业务两层状态。Store 独立于 Processor 存在，
// 因此 Processor 崩溃恢复后（用同一 Store 重建）状态不丢失。
type Store struct {
	mu      sync.Mutex
	byMsgID map[string]string // messageID -> ticketID
	records map[string]*record
}

// NewStore 创建空的内存存储。
func NewStore() *Store {
	return &Store{
		byMsgID: make(map[string]string),
		records: make(map[string]*record),
	}
}

func (s *Store) findByMessage(messageID string) (*record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticketID, ok := s.byMsgID[messageID]
	if !ok {
		return nil, false
	}
	rec, ok := s.records[ticketID]
	return rec, ok
}

func (s *Store) insert(rec *record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.ticket.ID] = rec
	s.byMsgID[rec.ticket.MessageID] = rec.ticket.ID
}

func (s *Store) get(ticketID string) (*record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[ticketID]
	return rec, ok
}
